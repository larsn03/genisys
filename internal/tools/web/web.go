// Package web implements web_search (SPEC §9.5) over the SearXNG client.
package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/larsn03/genisys/internal/tools"
	"github.com/larsn03/genisys/internal/websearch"
)

// Search is the web_search tool; C must be non-nil (the registry only
// registers it when web search is enabled and configured).
type Search struct{ C *websearch.Client }

func (Search) Name() string { return "web_search" }
func (Search) Description() string {
	return "Search the web. Returns numbered results (title, URL, snippet). Provide one objective and 2–3 concise keyword queries; use web_extract to read promising pages in full."
}
func (Search) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"objective":{"type":"string","description":"Natural-language research goal incl. source/freshness guidance."},
"searchQueries":{"type":"array","minItems":1,"maxItems":5,"items":{"type":"string","maxLength":200},
 "description":"Concise keyword queries, 3–6 words each; 2–3 recommended."}},
"required":["objective","searchQueries"],"additionalProperties":false}`)
}

func (s Search) Execute(ctx context.Context, raw json.RawMessage, _ *tools.World) tools.Result {
	var a struct {
		Objective     string   `json:"objective"`
		SearchQueries []string `json:"searchQueries"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil {
		return tools.Result{Content: "invalid_json: " + err.Error(), IsError: true}
	}
	if len(a.SearchQueries) == 0 {
		return tools.Result{Content: "invalid_args: searchQueries is required", IsError: true}
	}
	results, err := s.C.Search(ctx, a.SearchQueries)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	if len(results) == 0 {
		return tools.Result{Content: "no results."}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d results for: %s\n\n", len(results), a.Objective)
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Content)
	}
	return tools.Result{Content: b.String()}
}
