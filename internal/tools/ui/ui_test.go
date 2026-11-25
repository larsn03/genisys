package ui_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/larsn03/genisys/internal/agent/events"
	"github.com/larsn03/genisys/internal/model"
	"github.com/larsn03/genisys/internal/tools"
	"github.com/larsn03/genisys/internal/tools/ui"
	"github.com/larsn03/genisys/internal/workspace"
)

// P4-5: scratchpad → workspace delivery through the full grant/checkpoint
// writer (the run_python → deliver_file_to_user path).
func TestDeliverScratchpadToWorkspace(t *testing.T) {
	cwd, scratch := t.TempDir(), t.TempDir()
	g, err := workspace.NewGrants(cwd, scratch, true)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var evs []events.Event
	w := workspace.NewWriter(workspace.WriterOpts{
		Grants: g,
		Emit: events.SinkFunc(func(e events.Event) {
			mu.Lock()
			defer mu.Unlock()
			evs = append(evs, e)
		}),
	})
	reg := tools.NewRegistry()
	for _, tl := range ui.Tools() {
		reg.Add(tl)
	}
	world := &tools.World{Cwd: cwd, Scratchpad: scratch, Writer: w, StepID: "step_1",
		Emit: events.SinkFunc(func(e events.Event) {
			mu.Lock()
			defer mu.Unlock()
			evs = append(evs, e)
		})}

	// pretend run_python produced a deck in the scratchpad
	srcDir := filepath.Join(scratch, "outputs", "step_1-ab")
	os.MkdirAll(srcDir, 0o755)
	os.WriteFile(filepath.Join(srcDir, "deck.pptx"), []byte("PK\x03\x04fake"), 0o644)

	call := func(args string) tools.Result {
		return reg.Execute(context.Background(), model.ToolCall{
			ID: "c1", Type: "function",
			Function: model.ToolCallFunction{Name: "deliver_file_to_user", Arguments: args},
		}, world)
	}

	res := call(`{"source_path":"` + filepath.ToSlash(filepath.Join(srcDir, "deck.pptx")) + `","target_path":"deck.pptx"}`)
	if res.IsError {
		t.Fatalf("deliver failed: %s", res.Content)
	}
	b, err := os.ReadFile(filepath.Join(cwd, "deck.pptx"))
	if err != nil || !strings.HasPrefix(string(b), "PK") {
		t.Fatalf("delivered file missing/wrong: %v", err)
	}

	// overwrite rule
	if res = call(`{"source_path":"` + filepath.ToSlash(filepath.Join(srcDir, "deck.pptx")) + `","target_path":"deck.pptx"}`); !res.IsError || !strings.Contains(res.Content, "target exists") {
		t.Errorf("expected target-exists error, got %q", res.Content)
	}
	if res = call(`{"source_path":"` + filepath.ToSlash(filepath.Join(srcDir, "deck.pptx")) + `","target_path":"deck.pptx","overwrite":true}`); res.IsError {
		t.Errorf("overwrite deliver failed: %s", res.Content)
	}

	mu.Lock()
	defer mu.Unlock()
	var delivered, changed int
	for _, e := range evs {
		switch e.Type {
		case "ui_deliver_file":
			delivered++
		case "file_changed":
			changed++
		}
	}
	if delivered != 2 { // the target-exists attempt errors before emitting
		t.Errorf("ui_deliver_file events=%d want 2", delivered)
	}
	if changed < 1 { // identical overwrite produces no diff → no event
		t.Errorf("file_changed events=%d want >=1", changed)
	}
}
