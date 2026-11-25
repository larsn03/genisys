// Package find implements agentic_find (SPEC §9.4/§12): parallel
// read-only locator sub-agents with per-query failure isolation.
package find

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/larsn03/genisys/internal/tools"
)

type Find struct{}

func (Find) Name() string { return "agentic_find" }
func (Find) Description() string {
	return "Spawn read-only locator sub-agents to FIND code/files (not to analyze). Up to 4 queries run in parallel; each returns a short findings summary with file:line citations. Relative cwd resolves from the session working directory."
}
func (Find) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"queries":{"type":"array","minItems":1,"maxItems":4,"items":{
"type":"object","properties":{"cwd":{"type":"string","description":"Relative paths resolve from parent session cwd."},
"target":{"type":"string","description":"The specific locating task."}},
"required":["cwd","target"],"additionalProperties":false}}},
"required":["queries"],"additionalProperties":false}`)
}

func (Find) Execute(ctx context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Queries []struct {
			Cwd    string `json:"cwd"`
			Target string `json:"target"`
		} `json:"queries"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil {
		return tools.Result{Content: "invalid_json: " + err.Error(), IsError: true}
	}
	if len(a.Queries) == 0 {
		return tools.Result{Content: "invalid_args: queries is required", IsError: true}
	}
	if w.Subagent == nil {
		return tools.Result{Content: "error: sub-agents unavailable on this endpoint", IsError: true}
	}
	if len(a.Queries) > 4 {
		a.Queries = a.Queries[:4]
	}

	// SPEC §12: queries run concurrently; one failure never sinks the rest.
	out := make([]string, len(a.Queries))
	var wg sync.WaitGroup
	for i, q := range a.Queries {
		wg.Add(1)
		go func(i int, cwd, target string) {
			defer wg.Done()
			defer func() {
				if rec := recover(); rec != nil {
					out[i] = fmt.Sprintf("[query failed: panic %v]", rec)
				}
			}()
			out[i] = w.Subagent(ctx, cwd, target)
		}(i, q.Cwd, q.Target)
	}
	wg.Wait()

	var b strings.Builder
	for i, q := range a.Queries {
		fmt.Fprintf(&b, "## Query %d: %s (cwd: %s)\n\n%s\n\n", i+1, q.Target, orDot(q.Cwd), strings.TrimSpace(out[i]))
	}
	return tools.Result{Content: b.String()}
}

func orDot(s string) string {
	if s == "" {
		return "."
	}
	return s
}
