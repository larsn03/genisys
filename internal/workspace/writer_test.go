package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/larsn03/genisys/internal/agent/events"
)

func newTestWriter(t *testing.T) (*Writer, string, *[]events.Event) {
	t.Helper()
	cwd, scratch, _ := tree(t)
	g := grants(t, cwd, scratch, true)
	var mu sync.Mutex
	var evs []events.Event
	w := NewWriter(WriterOpts{Grants: g, Emit: events.SinkFunc(func(e events.Event) {
		mu.Lock()
		defer mu.Unlock()
		evs = append(evs, e)
	})})
	return w, cwd, &evs
}

func TestWriterReadBeforeWrite(t *testing.T) {
	w, cwd, _ := newTestWriter(t)
	f := filepath.Join(cwd, "a.txt")
	os.WriteFile(f, []byte("hello\n"), 0o644)

	if err := w.CheckRead(f); err == nil {
		t.Error("write without read must fail")
	}
	w.MarkRead(f)
	if err := w.CheckRead(f); err != nil {
		t.Errorf("after read: %v", err)
	}
	// user edits the file behind our back
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(f, []byte("user edit\n"), 0o644)
	if err := w.CheckRead(f); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Errorf("stale read must fail: %v", err)
	}
	w.MarkRead(f)
	if err := w.CheckRead(f); err != nil {
		t.Errorf("re-read fixes it: %v", err)
	}
}

func TestWriterMutateEmitsDiff(t *testing.T) {
	w, cwd, evs := newTestWriter(t)
	f := filepath.Join(cwd, "a.txt")
	os.WriteFile(f, []byte("one\ntwo\nthree\n"), 0o644)
	w.MarkRead(f)

	err := w.Mutate(context.Background(), "step_1", "before edit_file_tool(a.txt)", []string{f},
		func(t *Txn) error { return os.WriteFile(f, []byte("one\n2.5\ntwo\nthree\n"), 0o644) })
	if err != nil {
		t.Fatal(err)
	}
	var changed bool
	for _, e := range *evs {
		if e.Type == events.FileChanged {
			p := e.Payload.(map[string]any)
			if p["change"] == "modified" && strings.Contains(p["diff"].(string), "+2.5") {
				changed = true
			}
		}
	}
	if !changed {
		t.Errorf("no file_changed diff event: %+v", *evs)
	}
}

// P2-4 DoD: an interrupt arriving mid-write never leaves a half-written
// workspace — the critical section runs to completion.
func TestWriterCriticalSectionNotPreempted(t *testing.T) {
	w, cwd, _ := newTestWriter(t)
	f := filepath.Join(cwd, "a.txt")
	os.WriteFile(f, []byte("start\n"), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- w.Mutate(ctx, "step_1", "slow write", []string{f}, func(t *Txn) error {
			// simulate a multi-step write that a cancel must not split
			os.WriteFile(f, []byte("half\n"), 0o644)
			time.Sleep(50 * time.Millisecond)
			os.WriteFile(f, []byte("complete\n"), 0o644)
			return nil
		})
	}()
	time.Sleep(10 * time.Millisecond) // inside the critical section
	cancel()                          // interrupt arrives mid-write
	if err := <-done; err != nil {
		t.Fatalf("mutate returned %v", err)
	}
	b, _ := os.ReadFile(f)
	if string(b) != "complete\n" {
		t.Errorf("workspace half-written: %q", b)
	}
	// a NEW mutation after cancel must refuse to start
	err := w.Mutate(ctx, "step_2", "after cancel", []string{f}, func(t *Txn) error { return nil })
	if !errors.Is(err, ErrDenied) {
		t.Errorf("post-cancel mutate = %v, want denied", err)
	}
}
