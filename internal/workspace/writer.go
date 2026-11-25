package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pmezard/go-difflib/difflib"

	"github.com/larsn03/genisys/internal/agent/events"
	"github.com/larsn03/genisys/internal/store"
)

// Checkpointer snapshots roots before mutation (SPEC §11). Implemented by
// the go-git shadow repo (checkpoint.go) and by nil (no snapshots).
type Checkpointer interface {
	// Commit stages the given relative paths and commits; returns the sha.
	Commit(stepID, label string, relPaths []string) (string, error)
	// Restore writes the paths back to their state at sha.
	Restore(sha string, relPaths []string) error
}

// ReadState records a file's content hash at read time (SPEC §7.2
// read-before-write).
type ReadState struct {
	Hash  string
	Size  int64
	Mtime time.Time
}

// Writer is the single mutation path (SPEC §7.2 idempotent mutation path).
type Writer struct {
	mu sync.Mutex // critical section: non-preemptible (SPEC §7.4)

	grants    *Grants
	cp        Checkpointer
	db        *store.DB
	projectID string
	sessionID string
	emit      events.Sink

	reads map[string]ReadState // canonical path -> state at last read
}

// WriterOpts configures a Writer.
type WriterOpts struct {
	Grants    *Grants
	Cp        Checkpointer
	DB        *store.DB
	ProjectID string
	SessionID string
	Emit      events.Sink
}

// NewWriter wires a Writer; nil Cp/DB are fine (tests, free chat).
func NewWriter(o WriterOpts) *Writer {
	emit := o.Emit
	if emit == nil {
		emit = events.Discard
	}
	return &Writer{grants: o.Grants, cp: o.Cp, db: o.DB,
		projectID: o.ProjectID, sessionID: o.SessionID,
		emit: emit, reads: map[string]ReadState{}}
}

// Grants exposes the access table.
func (w *Writer) Grants() *Grants { return w.grants }

// MarkRead records the read-before-write witness for a file (called by the
// read tools through World).
func (w *Writer) MarkRead(path string) {
	st, err := statState(path)
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reads[path] = st
}

// CheckRead enforces the read-before-write rule: the file must have been
// read this session and be unchanged since (hash+mtime).
func (w *Writer) CheckRead(path string) error {
	w.mu.Lock()
	st, seen := w.reads[path]
	w.mu.Unlock()
	if !seen {
		return fmt.Errorf("read-before-write: you must read %q in this session before writing it (read_file_lines or read_file_chars)", display(path))
	}
	now, err := statState(path)
	if err != nil {
		return err
	}
	if now.Hash != st.Hash || now.Mtime != st.Mtime {
		return fmt.Errorf("read-before-write: %q changed since you read it — read it again first", display(path))
	}
	return nil
}

func statState(path string) (ReadState, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ReadState{}, err
	}
	mtime := time.Now()
	if fi, err := os.Stat(path); err == nil {
		mtime = fi.ModTime()
	}
	sum := sha256.Sum256(b)
	return ReadState{Hash: hex.EncodeToString(sum[:]), Size: int64(len(b)), Mtime: mtime}, nil
}

// Snapshot records a checkpoint of the given canonical paths without
// mutating anything (used before run_python output writes and by revert
// tooling). Returns "" when no checkpointer is wired.
func (w *Writer) Snapshot(stepID, label string, paths []string) (string, error) {
	if w.cp == nil {
		return "", nil
	}
	rel := make([]string, 0, len(paths))
	for _, p := range paths {
		if r, err := filepath.Rel(w.grants.CWD(), p); err == nil {
			rel = append(rel, r)
		}
	}
	sha, err := w.cp.Commit(stepID, label, rel)
	if err != nil {
		return "", err
	}
	if sha == "" {
		return "", nil // clean tree: nothing new to snapshot
	}
	if w.db != nil {
		_, _ = w.db.AppendCheckpoint(w.projectID, w.sessionID, stepID, sha, label)
	}
	w.emit.Emit(events.Event{Type: events.CheckpointCreated, Payload: map[string]any{"step": stepID, "sha": sha, "label": label}})
	return sha, nil
}

// Restore rolls the given canonical paths back to a checkpoint sha.
func (w *Writer) Restore(sha string, paths []string) error {
	if w.cp == nil {
		return fmt.Errorf("workspace: no checkpointer wired")
	}
	rel := make([]string, 0, len(paths))
	for _, p := range paths {
		if r, err := filepath.Rel(w.grants.CWD(), p); err == nil {
			rel = append(rel, r)
		}
	}
	if err := w.cp.Restore(sha, rel); err != nil {
		return err
	}
	for _, p := range paths {
		w.MarkRead(p) // post-revert files are "as read"
	}
	w.emit.Emit(events.Event{Type: events.CheckpointReverted, Payload: map[string]any{"sha": sha, "paths": len(paths)}})
	return nil
}

// Txn is handed to the Mutate closure: it performs FS operations inside the
// critical section with grants already enforced.
type Txn struct {
	w      *Writer
	step   string
	rel    []string // touched paths, relative to cwd (for checkpoint)
	before map[string][]byte
}

// Touch registers a canonical path as part of this mutation (checkpoint +
// diff scope). Call before modifying.
func (t *Txn) Touch(canonicalPath string) error {
	rel, err := filepath.Rel(t.w.grants.CWD(), canonicalPath)
	if err != nil || len(rel) >= 2 && rel[:2] == ".." {
		rel = canonicalPath // outside cwd: checkpoint skipped via abs path
	}
	t.rel = append(t.rel, rel)
	if b, err := os.ReadFile(canonicalPath); err == nil {
		if t.before == nil {
			t.before = map[string][]byte{}
		}
		t.before[canonicalPath] = b
	}
	return nil
}

// ErrSkipMutate aborts a Mutate without checkpoint churn.
var ErrSkipMutate = errors.New("workspace: skip mutate")

// Mutate runs the critical section: ctx is honored BEFORE entering; once
// inside, cancellation cannot preempt (workspace is never half-written).
// label describes the change for the checkpoint ledger ("before
// edit_file_tool(index.html)"). paths (canonical) scope the snapshot.
func (w *Writer) Mutate(ctx context.Context, stepID, label string, paths []string, fn func(*Txn) error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: mutation skipped before start", ErrDenied)
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	// pre-mutation checkpoint (SPEC §11)
	if _, err := w.Snapshot(stepID, label, paths); err != nil {
		return fmt.Errorf("checkpoint before %s: %w", label, err)
	}

	txn := &Txn{w: w, step: stepID}
	for _, p := range paths {
		_ = txn.Touch(p)
	}
	if err := fn(txn); err != nil {
		if errors.Is(err, ErrSkipMutate) {
			return nil
		}
		return err
	}

	// diff capture + file_changed events (SPEC §7.2 d)
	for _, p := range paths {
		old := txn.before[p]
		neu, err := os.ReadFile(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			continue
		}
		change := "modified"
		switch {
		case old == nil && err == nil:
			change = "created"
		case err != nil:
			change = "deleted"
		case string(old) == string(neu):
			continue
		}
		diff := unifiedDiff(display(p), old, neu)
		w.emit.Emit(events.Event{Type: events.FileChanged, Payload: map[string]any{
			"step": stepID, "path": display(p), "change": change, "diff": diff,
		}})
	}
	return nil
}

func unifiedDiff(name string, a, b []byte) string {
	d := difflib.UnifiedDiff{
		A: difflib.SplitLines(string(a)), B: difflib.SplitLines(string(b)),
		FromFile: name + " (before)", ToFile: name + " (after)", Context: 3,
	}
	s, _ := difflib.GetUnifiedDiffString(d)
	return s
}
