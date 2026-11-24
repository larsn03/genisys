package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/larsn03/genisys/internal/agent/events"
	"github.com/larsn03/genisys/internal/agent/parser"
	"github.com/larsn03/genisys/internal/model"
	"github.com/larsn03/genisys/internal/tools"
)

// Deps wires a session (constructor injection happens in cmd/, IMPL §2).
type Deps struct {
	Client     *model.Client
	Model      string
	Tools      *tools.Registry
	ToolsMode  string // native | xml | none (SPEC §13.2 profile)
	MaxSteps   int    // default 64 (SPEC §7.1)
	Cwd        string
	Scratchpad string
	Events     events.Sink
	Interj     *Interjector
	Retry      *model.RetryPolicy
}

// Session is one agent task thread (SPEC §7). P1 keeps the transcript in
// memory; P2 adds jsonl persistence + resume.
type Session struct {
	d        Deps
	messages []model.Message
	steps    int
	lastSig  string
	repeats  int
}

// NewSession builds a session with defaults filled.
func NewSession(d Deps) *Session {
	if d.MaxSteps <= 0 {
		d.MaxSteps = 64
	}
	if d.Events == nil {
		d.Events = events.Discard
	}
	if d.Interj == nil {
		d.Interj = NewInterjector()
	}
	if d.ToolsMode == "" {
		d.ToolsMode = "native"
	}
	return &Session{d: d}
}

// Interjections exposes the session queue (CLI/WS push into it).
func (s *Session) Interjections() *Interjector { return s.d.Interj }

// Messages returns a copy of the conversation (without the system prompt,
// which is re-rendered per request — SPEC §8).
func (s *Session) Messages() []model.Message {
	return append([]model.Message(nil), s.messages...)
}

// Run appends a user turn and drives the loop until a text-only answer,
// the step budget, or ctx cancellation.
func (s *Session) Run(ctx context.Context, userText string) (string, error) {
	s.messages = append(s.messages, model.Message{Role: "user", Content: userText})
	return s.loop(ctx)
}

func (s *Session) emit(t string, payload any) {
	s.d.Events.Emit(events.Event{Type: t, Payload: payload})
}

func (s *Session) loop(ctx context.Context) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// Delivery boundary (SPEC §7.4): interrupts first (they may have
		// cancelled the previous step), then queued messages.
		s.inject(s.d.Interj.TakeInterrupts(), "interrupt")
		s.inject(s.d.Interj.DrainBoundary(), "tool_batch")

		if s.steps >= s.d.MaxSteps {
			return s.wrapUp(ctx)
		}
		s.steps++
		stepID := fmt.Sprintf("step_%d", s.steps)
		s.emit(events.StepStarted, map[string]any{"step": stepID, "model": s.d.Model})

		// One cancellable step = model call + tool execution (SPEC §7.4:
		// an interrupt kills the in-flight action, whichever it is).
		stepCtx, cancel := context.WithCancel(ctx)
		go func() {
			select {
			case <-s.d.Interj.Notify():
				s.emit(events.InterruptStarted, map[string]any{"step": stepID})
				cancel()
			case <-stepCtx.Done():
			}
		}()

		text, calls, err := s.oneStep(stepCtx, stepID)
		if err != nil {
			if stepCtx.Err() != nil && ctx.Err() == nil {
				cancel()
				continue // interrupt: re-enter loop, message injected at top
			}
			cancel()
			return "", err
		}
		if len(calls) == 0 {
			cancel()
			s.emit(events.StepFinished, map[string]any{"step": stepID, "stop_reason": "end_turn"})
			return text, nil
		}

		// Loop detection (SPEC §7.2): identical batch 3× in a row → nudge.
		if sig := signature(calls); sig == s.lastSig {
			s.repeats++
			if s.repeats >= 2 {
				s.messages = append(s.messages, model.Message{Role: "user",
					Content: "[harness] You have repeated the same tool call three times. Change strategy or explain the obstacle."})
				s.repeats, s.lastSig = 0, ""
			}
		} else {
			s.repeats, s.lastSig = 0, sig
		}

		s.runTools(stepCtx, stepID, calls)
		cancel()
		s.emit(events.StepFinished, map[string]any{"step": stepID, "stop_reason": "tool_use"})
	}
}

func (s *Session) inject(ijs []Interjection, boundary string) {
	for _, ij := range ijs {
		s.messages = append(s.messages, model.Message{Role: "user", Content: InterjectionPrefix + ij.Text})
		s.emit(events.InterjectionDelivered, map[string]any{"id": ij.ID, "boundary": boundary})
	}
}

// oneStep renders the prompt, calls the endpoint, parses the reply and
// commits the assistant message. Returns (prose, tool calls); a nil-prose
// nil-calls return with nil error means "interrupted, re-enter the loop".
func (s *Session) oneStep(stepCtx context.Context, stepID string) (string, []model.ToolCall, error) {
	sysPrompt, err := RenderSystem(PromptEnv{
		Cwd: s.d.Cwd, Scratchpad: s.d.Scratchpad,
		ToolsMode: s.d.ToolsMode, Tools: s.d.Tools.Definitions(),
	})
	if err != nil {
		return "", nil, err
	}
	req := model.ChatRequest{
		Model:    s.d.Model,
		Messages: append([]model.Message{{Role: "system", Content: sysPrompt}}, s.messages...),
	}
	if s.d.ToolsMode == "native" {
		req.Tools = s.d.Tools.Definitions()
		req.ToolChoice = "auto"
	}

	comp, err := s.d.Client.CompleteWith(stepCtx, req, s.d.Retry)
	if comp != nil && comp.Usage.TotalTokens > 0 {
		s.emit(events.Usage, comp.Usage)
	}

	if err != nil {
		if stepCtx.Err() != nil {
			// SPEC §7.4: commit the partial, close pending calls honestly.
			if comp != nil && (comp.Text != "" || len(comp.ToolCalls) > 0) {
				s.messages = append(s.messages, assistantMessage(comp.Text, comp.ToolCalls))
				for _, tc := range comp.ToolCalls {
					s.messages = append(s.messages, model.Message{
						Role: "tool", ToolCallID: tc.ID, Content: InterruptedResult})
				}
			}
			return "", nil, nil // re-enter loop; inject happens at top
		}
		return "", nil, err // endpoint failure (P3 error cards)
	}

	prose, calls := s.parse(comp)
	s.messages = append(s.messages, assistantMessage(prose, calls))
	if prose != "" {
		s.emit(events.TextDelta, map[string]any{"step": stepID, "text": prose})
	}

	// An interrupt may have fired while the completion landed: close the
	// calls as interrupted instead of executing them (SPEC §7.4).
	if len(calls) > 0 && (stepCtx.Err() != nil || s.hasPendingInterrupts()) {
		for _, tc := range calls {
			s.messages = append(s.messages, model.Message{
				Role: "tool", ToolCallID: tc.ID, Content: InterruptedResult})
		}
		return "", nil, nil
	}
	return prose, calls, nil
}

// parse applies SPEC §13.4: native tool_calls win; otherwise the XML
// fallback extracts calls from the text (automatic in every mode).
func (s *Session) parse(comp *model.Completion) (string, []model.ToolCall) {
	if len(comp.ToolCalls) > 0 {
		return comp.Text, comp.ToolCalls
	}
	prose, calls, _ := parser.ParseText(comp.Text)
	if len(calls) == 0 {
		return comp.Text, nil
	}
	return prose, calls
}

// runTools executes a batch concurrently (independent calls, SPEC §7.1)
// and appends results in call order.
func (s *Session) runTools(ctx context.Context, stepID string, calls []model.ToolCall) {
	results := make([]tools.Result, len(calls))
	var wg sync.WaitGroup
	for i := range calls {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.emit(events.ToolStarted, map[string]any{"step": stepID, "call_id": calls[i].ID, "tool": calls[i].Function.Name, "args": firstLineOf(calls[i].Function.Arguments, 100)})
			res := s.d.Tools.Execute(ctx, calls[i], &tools.World{
				Cwd: s.d.Cwd, Scratchpad: s.d.Scratchpad, ShellTimeout: 10000, Emit: s.d.Events,
			})
			results[i] = res
			s.emit(events.ToolFinished, map[string]any{
				"step": stepID, "call_id": calls[i].ID, "ok": !res.IsError,
				"summary": firstLineOf(res.Content, 160),
			})
		}(i)
	}
	wg.Wait()
	for i, tc := range calls {
		s.messages = append(s.messages, model.Message{
			Role: "tool", ToolCallID: tc.ID, Content: results[i].Content})
	}
}

// wrapUp is the forced final turn when the step budget is exhausted
// (SPEC §7.2).
func (s *Session) wrapUp(ctx context.Context) (string, error) {
	s.messages = append(s.messages, model.Message{Role: "user",
		Content: "[harness] Step budget exhausted. Summarize the current state: what is done, what remains, and any blocker. Do not call tools."})
	sysPrompt, err := RenderSystem(PromptEnv{
		Cwd: s.d.Cwd, Scratchpad: s.d.Scratchpad,
		ToolsMode: s.d.ToolsMode, Tools: s.d.Tools.Definitions(),
	})
	if err != nil {
		return "", err
	}
	comp, err := s.d.Client.CompleteWith(ctx, model.ChatRequest{
		Model:    s.d.Model,
		Messages: append([]model.Message{{Role: "system", Content: sysPrompt}}, s.messages...),
	}, s.d.Retry)
	if err != nil {
		return "", err
	}
	prose, _ := s.parse(comp)
	s.messages = append(s.messages, model.Message{Role: "assistant", Content: prose})
	return prose, nil
}

// hasPendingInterrupts reports queued interrupt-mode interjections whose
// cancel signal may already have been consumed.
func (s *Session) hasPendingInterrupts() bool {
	for _, ij := range s.d.Interj.Pending() {
		if ij.Mode == ModeInterrupt {
			return true
		}
	}
	return false
}

func assistantMessage(prose string, calls []model.ToolCall) model.Message {
	m := model.Message{Role: "assistant", Content: prose}
	if len(calls) > 0 {
		m.ToolCalls = calls
	}
	return m
}

func signature(calls []model.ToolCall) string {
	var b strings.Builder
	for _, c := range calls {
		b.WriteString(c.Function.Name)
		b.WriteByte(0)
		b.WriteString(strings.TrimSpace(c.Function.Arguments))
		b.WriteByte(1)
	}
	return b.String()
}

func firstLineOf(s string, n int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// ErrInterrupted is returned by Run when the context was cancelled.
var ErrInterrupted = errors.New("interrupted")
