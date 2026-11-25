package py_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/larsn03/genisys/internal/agent/events"
	"github.com/larsn03/genisys/internal/tools"
	py "github.com/larsn03/genisys/internal/tools/py"
)

func world(t *testing.T) *tools.World {
	t.Helper()
	home := t.TempDir()
	return &tools.World{
		Cwd: t.TempDir(), Scratchpad: filepath.Join(home, "scratch"),
		RuntimeDir: filepath.Join(home, "runtime"),
		Emit:       events.Discard,
	}
}

func call(t *testing.T, w *tools.World, ctx context.Context, args string) tools.Result {
	t.Helper()
	return py.Tool{}.Execute(ctx, json.RawMessage(args), w)
}

// raceEnabled (race.go / norace.go): the wazero interpreter under the
// race detector is ~10× slower; budgets adapt.

func guestTimeoutMS() int {
	if raceEnabled() {
		return 300000
	}
	return 120000
}

func TestRunPythonDeck(t *testing.T) {
	w := world(t)
	res := call(t, w, context.Background(), fmt.Sprintf(`{"code":"from ppt_builder import Deck\nd=Deck(title='T')\nd.add_title('T')\nfor i in range(5): d.add_bullets('S'+str(i), ['x'])\nd.add_assumptions(['a'])\nd.save('/outputs/deck.pptx')\nprint('ok')","timeout_ms":%d}`, guestTimeoutMS()))
	if res.IsError {
		t.Fatalf("error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "ok") {
		t.Errorf("stdout missing: %s", res.Content)
	}
	idx := strings.Index(res.Content, "- ")
	if idx < 0 {
		t.Fatalf("no output file listed: %s", res.Content)
	}
	outPath := strings.TrimSpace(res.Content[idx+2:])
	fi, err := os.Stat(outPath)
	if err != nil || fi.Size() < 1000 {
		t.Fatalf("deck missing at %s (%v)", outPath, err)
	}
	if !strings.HasPrefix(outPath, w.Scratchpad) {
		t.Errorf("output not under scratchpad: %s", outPath)
	}
}

func TestStageFilesAndInputs(t *testing.T) {
	w := world(t)
	att := filepath.Join(w.Scratchpad, "attachments")
	os.MkdirAll(att, 0o755)
	os.WriteFile(filepath.Join(att, "data.csv"), []byte("a,b\n1,2\n3,4\n"), 0o644)
	res := call(t, w, context.Background(), fmt.Sprintf(`{"code":"import csv\nrows=list(csv.reader(open('/inputs/data.csv')))\nprint(sum(int(r[0])+int(r[1]) for r in rows[1:]))","stage_files":["data.csv"],"timeout_ms":%d}`, guestTimeoutMS()))
	if res.IsError || !strings.Contains(res.Content, "10") {
		t.Fatalf("staged input failed: err=%v %s", res.IsError, res.Content)
	}
}

func TestInterruptKillsGuest(t *testing.T) {
	w := world(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(1500 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	res := call(t, w, ctx, fmt.Sprintf(`{"code":"x=0\nwhile True: x+=1","timeout_ms":%d}`, guestTimeoutMS()))
	elapsed := time.Since(start)
	limit := 8 * time.Second
	if raceEnabled() {
		limit = 40 * time.Second // JIT-free but race-instrumented startup is slow
	}
	if elapsed > limit {
		t.Fatalf("guest survived the interrupt: %v", elapsed)
	}
	if !res.IsError {
		t.Errorf("expected error result for interrupted run: %s", res.Content)
	}
	t.Logf("killed after %v: %s", elapsed, strings.TrimSpace(res.Content))
}

func TestLoadAttachment(t *testing.T) {
	w := world(t)
	att := filepath.Join(w.Scratchpad, "attachments")
	os.MkdirAll(att, 0o755)
	os.WriteFile(filepath.Join(att, "memo.txt"), []byte("dear board ..."), 0o644)
	res := py.LoadAttachment{}.Execute(context.Background(),
		json.RawMessage(`{"path":"memo.txt"}`), w)
	if res.IsError || !strings.Contains(res.Content, "dear board") {
		t.Fatalf("load failed: err=%v %s", res.IsError, res.Content)
	}
	// xlsx gets the run_python hint
	os.WriteFile(filepath.Join(att, "s.xlsx"), []byte("PK\x03\x04junk"), 0o644)
	res = py.LoadAttachment{}.Execute(context.Background(),
		json.RawMessage(`{"path":"s.xlsx"}`), w)
	if res.IsError || !strings.Contains(res.Content, "read_xlsx") {
		t.Fatalf("xlsx hint missing: %s", res.Content)
	}
}
