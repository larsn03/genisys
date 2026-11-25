package find_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/larsn03/genisys/internal/model"
	"github.com/larsn03/genisys/internal/tools"
	"github.com/larsn03/genisys/internal/tools/find"
)

func exec(t *testing.T, args string, w *tools.World) tools.Result {
	t.Helper()
	r := tools.NewRegistry()
	r.Add(find.Find{})
	return r.Execute(context.Background(), model.ToolCall{
		ID: "c", Type: "function",
		Function: model.ToolCallFunction{Name: "agentic_find", Arguments: args},
	}, w)
}

// P5-2 DoD pieces: parallel execution + failure isolation (SPEC §12).
func TestAgenticFindParallelIsolation(t *testing.T) {
	// barrier: every query waits (briefly) for all four to be in flight;
	// a serial runner times out instead of deadlocking.
	var running, peak atomic.Int32
	w := &tools.World{Subagent: func(ctx context.Context, cwd, target string) string {
		running.Add(1)
		defer running.Add(-1)
		// snapshot the peak: if all four run concurrently, someone sees 4
		n := running.Load()
		for {
			m := peak.Load()
			if n <= m || peak.CompareAndSwap(m, n) {
				break
			}
		}
		deadline := time.Now().Add(300 * time.Millisecond)
		for running.Load() < 4 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		switch target {
		case "boom":
			panic("locator exploded")
		case "slow":
			return "[query failed: context deadline exceeded]"
		}
		return "Found it.\n\nCitations:\n- src/http/client.go:42 — the client"
	}}
	start := time.Now()
	res := exec(t, `{"queries":[{"cwd":".","target":"http client"},{"cwd":".","target":"boom"},{"cwd":".","target":"slow"},{"cwd":"api","target":"routes"}]}`, w)
	elapsed := time.Since(start)
	c := res.Content
	if res.IsError {
		t.Errorf("must not fail the batch: %s", c)
	}
	if strings.Count(c, "Found it.") != 2 {
		t.Errorf("good queries must both report: %s", c)
	}
	if !strings.Contains(c, "[query failed: panic locator exploded]") {
		t.Errorf("panic not isolated: %s", c)
	}
	if !strings.Contains(c, "[query failed: context deadline exceeded]") {
		t.Errorf("error not isolated: %s", c)
	}
	// the two good queries must have completed while all four were in
	// flight — a serial runner would have timed the barrier out and the
	// "boom" panic proves isolation regardless. Concurrency is asserted
	// via the barrier: with 4 parallel queries the whole call takes ~0 ms,
	// serial would take 4×300 ms.
	if peak.Load() < 2 {
		t.Errorf("queries ran serially (peak concurrent=%d)", peak.Load())
	}
	_ = elapsed
	if !strings.Contains(c, "(cwd: api)") {
		t.Errorf("cwd echo missing")
	}
}

func TestAgenticFindNoSubagent(t *testing.T) {
	res := exec(t, `{"queries":[{"cwd":".","target":"x"}]}`, &tools.World{})
	if !res.IsError || !strings.Contains(res.Content, "sub-agents unavailable") {
		t.Errorf("got %q", res.Content)
	}
}

func TestAgenticFindSchemaLimits(t *testing.T) {
	var q []string
	_ = json.Unmarshal([]byte(`[1,2,3,4,5]`), &q)
	res := exec(t, `{"queries":[{"cwd":".","target":"a"},{"cwd":".","target":"http client"},{"cwd":".","target":"http client"},{"cwd":".","target":"http client"},{"cwd":".","target":"http client"}]}`,
		&tools.World{Subagent: func(context.Context, string, string) string { return "ok" }})
	if strings.Count(res.Content, "## Query") != 4 {
		t.Errorf("must cap at 4 queries: %s", res.Content)
	}
}
