// Package supervisor runs session workers (SPEC §19): one cancellable
// goroutine per running session, a concurrency cap with FIFO queue, the
// per-session event hub (ledger + live fan-out), and the permission /
// interjection plumbing the wire protocol needs. The UI is a pure
// subscriber: closing a tab never stops a worker.
package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/larsn03/genisys/internal/agent"
	"github.com/larsn03/genisys/internal/agent/events"
	"github.com/larsn03/genisys/internal/app"
	"github.com/larsn03/genisys/internal/store"
	"github.com/larsn03/genisys/internal/workspace"
)

// Hub broadcasts session events: every publish is written to the events
// ledger first (SPEC §21: replayable), then fanned out live with the
// ledger id attached so reconnecting clients can dedupe.
type Hub struct {
	mu   sync.Mutex
	db   *store.DB
	sess string
	subs map[int]chan Entry
	next int
}

// Entry is one broadcast event with its ledger id (0 when unpersisted).
type Entry struct {
	ID      int64          `json:"id"`
	Session string         `json:"session_id"`
	StepID  string         `json:"step_id,omitempty"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

// NewHub builds a hub for one session.
func NewHub(db *store.DB, sessionID string) *Hub {
	return &Hub{db: db, sess: sessionID, subs: map[int]chan Entry{}}
}

// Publish records and broadcasts one event; returns the ledger id.
func (h *Hub) Publish(typ, stepID string, payload any) int64 {
	var id int64
	if h.db != nil {
		if err := h.db.AppendEvent(h.sess, stepID, typ, payload); err == nil {
			id = h.db.LastEventID()
		}
	}
	p, _ := payload.(map[string]any)
	if p == nil && payload != nil {
		// struct payloads (model.Usage…) → their JSON object form
		if b, err := json.Marshal(payload); err == nil {
			_ = json.Unmarshal(b, &p)
		}
	}
	e := Entry{ID: id, Session: h.sess, StepID: stepID, Type: typ, Payload: p}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs {
		select {
		case ch <- e:
		default: // slow consumer: drop; ledger replay covers gaps
		}
	}
	return id
}

// Subscribe registers a live subscriber.
func (h *Hub) Subscribe() (int, <-chan Entry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	ch := make(chan Entry, 256)
	h.subs[h.next] = ch
	return h.next, ch
}

// Unsubscribe removes a subscriber.
func (h *Hub) Unsubscribe(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.subs[id]; ok {
		delete(h.subs, id)
		close(ch)
	}
}

// PromptResult describes what Prompt did.
type PromptResult int

// Prompt outcomes.
const (
	Started PromptResult = iota
	Interjected
	Queued
)

// Worker owns one session's runtime.
type Worker struct {
	ID     string
	Row    *store.Session
	Hub    *Hub
	Interj *agent.Interjector

	sup     *Supervisor
	openFn  func() (*app.Handle, error)
	mu      sync.Mutex
	handle  *app.Handle
	running bool
	cancel  context.CancelFunc
	permR   map[int64]chan bool
}

// Supervisor is the worker registry + concurrency scheduler (SPEC §19).
type Supervisor struct {
	App *app.App
	Cap int

	mu      sync.Mutex
	workers map[string]*Worker
	sem     chan struct{}
	queue   []func()
}

// New builds a supervisor with a concurrency cap (SPEC §19: 4).
func New(a *app.App, maxConcurrent int) *Supervisor {
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	return &Supervisor{
		App: a, Cap: maxConcurrent,
		workers: map[string]*Worker{},
		sem:     make(chan struct{}, maxConcurrent),
	}
}

// Worker returns the worker for a session id, creating it on demand.
func (s *Supervisor) Worker(sessionID string) (*Worker, error) {
	s.mu.Lock()
	if w, ok := s.workers[sessionID]; ok {
		s.mu.Unlock()
		return w, nil
	}
	s.mu.Unlock()

	row, err := s.App.DB.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	var proj *store.Project
	if row.ProjectID != "" {
		if proj, err = s.App.DB.GetProject(row.ProjectID); err != nil {
			return nil, err
		}
	}
	w := &Worker{
		ID: sessionID, Row: row, sup: s,
		Hub:    NewHub(s.App.DB, sessionID),
		Interj: agent.NewInterjector(),
		permR:  map[int64]chan bool{},
	}
	w.openFn = func() (*app.Handle, error) {
		opts := app.SessionOpts{Interj: w.Interj,
			Emit:     events.SinkFunc(w.emit),
			Ask:      w.ask,
			Registry: s.App.Registry(true),
		}
		if proj == nil { // free chat: private per-session working dir
			dir := filepath.Join(s.App.Root, "freechat", sessionID)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
			opts.Cwd = dir
		}
		return s.App.OpenSession(row, proj, opts)
	}
	s.mu.Lock()
	if ex, ok := s.workers[sessionID]; ok { // lost a race
		s.mu.Unlock()
		return ex, nil
	}
	s.workers[sessionID] = w
	s.mu.Unlock()
	return w, nil
}

// Sessions lists known workers (for status endpoints).
func (s *Supervisor) RunningSessions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for id, w := range s.workers {
		if w.Running() {
			out = append(out, id)
		}
	}
	return out
}

// CloseAll stops every worker (server shutdown).
func (s *Supervisor) CloseAll() {
	s.mu.Lock()
	ws := make([]*Worker, 0, len(s.workers))
	for _, w := range s.workers {
		ws = append(ws, w)
	}
	s.mu.Unlock()
	for _, w := range ws {
		w.Close()
	}
}

// emit routes agent events to the hub.
func (w *Worker) emit(e events.Event) {
	stepID := ""
	if m, ok := e.Payload.(map[string]any); ok {
		if sid, ok := m["step"].(string); ok {
			stepID = sid
		}
	}
	w.Hub.Publish(e.Type, stepID, e.Payload)
}

// ask implements workspace.Asker over the wire: publish the card, wait
// for AnswerPermission or cancellation (interrupt = deny, SPEC §7.4).
func (w *Worker) ask(ctx context.Context, req workspace.PermRequest) bool {
	reply := make(chan bool, 1)
	w.mu.Lock()
	w.permR[req.ID] = reply
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.permR, req.ID)
		w.mu.Unlock()
	}()
	w.Hub.Publish(events.PermissionRequested, "", map[string]any{
		"id": req.ID, "path": req.Path, "mode": string(req.Mode), "reason": req.Reason,
	})
	select {
	case ok := <-reply:
		return ok
	case <-ctx.Done():
		return false
	}
}

// AnswerPermission resolves a pending card from the UI.
func (w *Worker) AnswerPermission(id int64, granted bool) bool {
	w.mu.Lock()
	ch, ok := w.permR[id]
	w.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- granted:
		return true
	default:
		return false
	}
}

// Prompt starts a turn, interjects a running one (SPEC §7.4 mode), or
// queues behind the concurrency cap.
func (w *Worker) Prompt(ctx context.Context, text, mode string) (PromptResult, error) {
	if w.Running() {
		m := agent.ModeQueue
		if mode == "interrupt" {
			m = agent.ModeInterrupt
		}
		ij := w.Interj.Push(text, m)
		w.Hub.Publish(events.InterjectionQueued, "", map[string]any{
			"id": ij.ID, "text": text, "mode": string(m),
		})
		return Interjected, nil
	}
	if !w.sup.tryAcquire() {
		w.sup.enqueue(func() { _, _ = w.Prompt(context.Background(), text, mode) })
		return Queued, nil
	}
	h, err := w.ensureHandle()
	if err != nil {
		w.sup.release()
		return Started, err
	}
	w.mu.Lock()
	w.running = true
	runCtx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.mu.Unlock()
	_ = w.sup.App.DB.SetSessionStatus(w.ID, store.StatusRunning)
	w.Hub.Publish(events.UserTurn, "", map[string]any{"text": text})
	w.Hub.Publish(events.SessionStatus, "", map[string]any{"status": store.StatusRunning})

	go func() {
		_, rerr := h.Sess.Run(runCtx, text)
		cancel()
		w.mu.Lock()
		w.running, w.cancel = false, nil
		w.mu.Unlock()
		w.sup.release()
		if rerr != nil && !errors.Is(rerr, context.Canceled) {
			w.Hub.Publish(events.SessionError, "", map[string]any{"error": rerr.Error()})
			_ = w.sup.App.DB.SetSessionStatus(w.ID, store.StatusError)
			w.Hub.Publish(events.SessionStatus, "", map[string]any{"status": store.StatusError})
			return
		}
		_ = w.sup.App.DB.SetSessionStatus(w.ID, store.StatusIdle)
		w.Hub.Publish(events.SessionStatus, "", map[string]any{"status": store.StatusIdle})
	}()
	return Started, nil
}

// Compact forces context compaction now (SPEC §24 manual action from the
// error card / settings). Refuses while a turn is in flight.
func (w *Worker) Compact(ctx context.Context) error {
	if w.Running() {
		return fmt.Errorf("supervisor: session is running — stop it before compacting")
	}
	h, err := w.ensureHandle()
	if err != nil {
		return err
	}
	return h.Sess.CompactNow(ctx)
}

// Stop cancels the running turn.
func (w *Worker) Stop() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running && w.cancel != nil {
		w.cancel()
		return true
	}
	return false
}

// Running reports worker state.
func (w *Worker) Running() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.running
}

// Interjections exposes the live queue (list/withdraw/promote).
func (w *Worker) Interjections() *agent.Interjector { return w.Interj }

// Close drops the worker's handle (db + ledger survive).
func (w *Worker) Close() {
	w.mu.Lock()
	if w.running && w.cancel != nil {
		w.cancel()
	}
	h := w.handle
	w.handle = nil
	w.mu.Unlock()
	if h != nil {
		h.Close()
	}
}

// Ensure opens the session handle (file endpoints need grants/writer).
func (w *Worker) Ensure() (*app.Handle, error) { return w.ensureHandle() }

func (w *Worker) ensureHandle() (*app.Handle, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.handle != nil {
		return w.handle, nil
	}
	h, err := w.openFn()
	if err != nil {
		return nil, err
	}
	w.handle = h
	return h, nil
}

// --- concurrency cap (SPEC §19: max 4 running, rest queued) ---------------

func (s *Supervisor) tryAcquire() bool {
	select {
	case s.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Supervisor) release() {
	<-s.sem
	s.runNext()
}

func (s *Supervisor) enqueue(fn func()) {
	s.mu.Lock()
	s.queue = append(s.queue, fn)
	s.mu.Unlock()
}

func (s *Supervisor) runNext() {
	s.mu.Lock()
	if len(s.queue) == 0 {
		s.mu.Unlock()
		return
	}
	fn := s.queue[0]
	s.queue = s.queue[1:]
	s.mu.Unlock()
	go fn()
}

// Queued reports the queue depth (status endpoint).
func (s *Supervisor) Queued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}
