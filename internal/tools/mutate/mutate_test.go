package mutate

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/larsn03/genisys/internal/tools"
	"github.com/larsn03/genisys/internal/workspace"
)

func world(t *testing.T) (*tools.World, string) {
	t.Helper()
	cwd := t.TempDir()
	scratch := t.TempDir()
	g, err := workspace.NewGrants(cwd, scratch, true)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := workspace.OpenShadow(filepath.Join(t.TempDir(), "shadow"), cwd)
	if err != nil {
		t.Fatal(err)
	}
	w := &tools.World{
		Cwd: cwd, Scratchpad: scratch, StepID: "step_1",
		Writer: workspace.NewWriter(workspace.WriterOpts{Grants: g, Cp: cp}),
	}
	_ = cp
	return w, cwd
}

func call(t *testing.T, tool tools.Tool, w *tools.World, args string) tools.Result {
	t.Helper()
	return tool.Execute(context.Background(), json.RawMessage(args), w)
}

func mustOK(t *testing.T, r tools.Result) {
	t.Helper()
	if r.IsError {
		t.Fatalf("unexpected error: %s", r.Content)
	}
}

func TestEditFileSemantics(t *testing.T) {
	w, cwd := world(t)
	f := filepath.Join(cwd, "a.txt")
	os.WriteFile(f, []byte("alpha\r\nbeta\ngamma\n"), 0o644)

	// not read yet → rejected
	r := call(t, EditFile{}, w, `{"path":"a.txt","edits":[{"search_text":"beta","replace_text":"B"}]}`)
	if !r.IsError || !strings.Contains(r.Content, "read-before-write") {
		t.Errorf("expected read-before-write error, got %q", r.Content)
	}

	// read via the real tool
	mr := call(t, &readShim{}, w, `{"path":"a.txt"}`)
	mustOK(t, mr)

	// CRLF-normalized match works
	r = call(t, EditFile{}, w, `{"path":"a.txt","edits":[{"search_text":"beta","replace_text":"BETA"}]}`)
	mustOK(t, r)
	b, _ := os.ReadFile(f)
	if string(b) != "alpha\nBETA\ngamma\n" {
		t.Errorf("content = %q", b)
	}

	// non-unique search → rejected, nothing applied
	r = call(t, EditFile{}, w, `{"path":"a.txt","edits":[{"search_text":"a","replace_text":"A"}]}`)
	if !r.IsError || !strings.Contains(r.Content, "matches") {
		t.Errorf("expected non-unique error, got %q", r.Content)
	}

	// all-or-nothing: second edit missing → first not applied either
	r = call(t, EditFile{}, w, `{"path":"a.txt","edits":[{"search_text":"alpha","replace_text":"ALPHA"},{"search_text":"nope","replace_text":"X"}]}`)
	if !r.IsError || !strings.Contains(r.Content, "no edits applied") {
		t.Errorf("expected all-or-nothing error, got %q", r.Content)
	}
	b, _ = os.ReadFile(f)
	if strings.Contains(string(b), "ALPHA") {
		t.Error("partial application despite rejection")
	}
}

// readShim reads a file through World access + MarkRead (stands in for
// read_file_lines without importing fileio, which would be fine too).
type readShim struct{}

func (readShim) Name() string        { return "read" }
func (readShim) Description() string { return "read" }
func (readShim) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
}
func (readShim) Execute(_ context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Path string `json:"path"`
	}
	json.Unmarshal(raw, &a)
	p, err := w.Access(a.Path, false)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	w.MarkRead(p)
	return tools.Result{Content: string(b)}
}

func TestReplaceDeleteMoveCopy(t *testing.T) {
	w, cwd := world(t)
	r := call(t, ReplaceFile{}, w, `{"path":"new/deep.txt","content":"hello"}`)
	mustOK(t, r)
	if b, _ := os.ReadFile(filepath.Join(cwd, "new", "deep.txt")); string(b) != "hello" {
		t.Errorf("created file = %q", b)
	}

	r = call(t, Copy{}, w, `{"source_path":"new/deep.txt","target_path":"copy.txt","can_overwrite":false}`)
	mustOK(t, r)
	r = call(t, Copy{}, w, `{"source_path":"new/deep.txt","target_path":"copy.txt","can_overwrite":false}`)
	if !r.IsError || !strings.Contains(r.Content, "target exists") {
		t.Errorf("overwrite guard: %q", r.Content)
	}

	r = call(t, Move{}, w, `{"source_path":"copy.txt","target_path":"moved.txt","can_overwrite":false}`)
	mustOK(t, r)
	if _, err := os.Stat(filepath.Join(cwd, "copy.txt")); !os.IsNotExist(err) {
		t.Error("move left source")
	}

	r = call(t, CreateFolder{}, w, `{"path":"afolder"}`)
	mustOK(t, r)
	if fi, err := os.Stat(filepath.Join(cwd, "afolder")); err != nil || !fi.IsDir() {
		t.Error("create_folder failed")
	}

	r = call(t, Delete{}, w, `{"path":"new"}`)
	mustOK(t, r)
	if _, err := os.Stat(filepath.Join(cwd, "new")); !os.IsNotExist(err) {
		t.Error("delete failed")
	}
}

// P2-6 DoD property test: a random sequence of edits, each checkpointed,
// replays exactly — restoring checkpoint k yields the file as it was
// before edit k+1.
func TestPropertyEditSequenceReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	w, cwd := world(t)
	f := filepath.Join(cwd, "doc.txt")
	os.WriteFile(f, []byte("l1\nl2\nl3\nl4\nl5\n"), 0o644)
	call(t, &readShim{}, w, `{"path":"doc.txt"}`)

	type snap struct {
		before string // content before the edit
		sha    string
	}
	var snaps []snap
	lines := []string{"l1", "l2", "l3", "l4", "l5"}

	for round := 0; round < 12; round++ {
		b, _ := os.ReadFile(f)
		s := &snap{before: string(b)}
		// checkpoint the current state (what Writer.Mutate does internally;
		// we drive the checkpointer directly to record the sha)
		sha, err := w.Writer.Snapshot("step_"+fmt.Sprint(round),
			"prop", []string{f})
		if err != nil {
			t.Fatal(err)
		}
		s.sha = sha
		snaps = append(snaps, *s)

		// random single-line edit
		idx := rng.Intn(len(lines))
		repl := fmt.Sprintf("x%d-%d", round, rng.Intn(100))
		r := call(t, EditFile{}, w, fmt.Sprintf(`{"path":"doc.txt","edits":[{"search_text":%q,"replace_text":%q}]}`, lines[idx], repl))
		mustOK(t, r)
		lines[idx] = repl
	}

	// replay every snapshot and compare
	for i, s := range snaps {
		if err := w.Writer.Restore(s.sha, []string{f}); err != nil {
			t.Fatalf("restore %d: %v", i, err)
		}
		b, _ := os.ReadFile(f)
		if string(b) != s.before {
			t.Errorf("snapshot %d mismatch:\n got %q\nwant %q", i, b, s.before)
		}
	}
}
