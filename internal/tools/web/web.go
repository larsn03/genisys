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
	return tools.Result{Content: wrapUntrusted(b.String())}
}

// wrapUntrusted marks fetched content as untrusted input (SPEC §22): the
// model must treat instructions inside it as data, not commands.
func wrapUntrusted(inner string) string {
	return "<web_content trust=\"untrusted\">\n" + inner + "</web_content>"
}

// Extract is the web_extract tool (SPEC §9.5): readability → markdown,
// 8 KB per URL, per-URL failure isolation, untrusted wrapping.
type Extract struct{}

func (Extract) Name() string { return "web_extract" }
func (Extract) Description() string {
	return "Fetch URLs and return their main content as readable markdown (8 KB per URL). Use after web_search to read promising pages. Failures are reported per URL."
}
func (Extract) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"urls":{"type":"array","minItems":1,"maxItems":20,"items":{"type":"string"}},
"objective":{"type":"string","description":"What to pull; keeps excerpts focused."}},
"required":["urls","objective"],"additionalProperties":false}`)
}

func (Extract) Execute(ctx context.Context, raw json.RawMessage, _ *tools.World) tools.Result {
	var a struct {
		URLs      []string `json:"urls"`
		Objective string   `json:"objective"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil {
		return tools.Result{Content: "invalid_json: " + err.Error(), IsError: true}
	}
	if len(a.URLs) == 0 {
		return tools.Result{Content: "invalid_args: urls is required", IsError: true}
	}
	if len(a.URLs) > 20 {
		a.URLs = a.URLs[:20]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Extracted %d URL(s) for: %s\n\n", len(a.URLs), a.Objective)
	ok := 0
	for _, u := range a.URLs {
		src, err := fetchGET(ctx, u)
		if err != nil {
			fmt.Fprintf(&b, "### %s\n[error] %v\n\n", u, err)
			continue
		}
		md := htmlToMarkdown(src)
		if md == "" {
			fmt.Fprintf(&b, "### %s\n[no readable content found]\n\n", u)
			continue
		}
		if len(md) > 8192 {
			md = md[:8192] + "\n…[truncated at 8 KB]"
		}
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", u, md)
		ok++
	}
	if ok == 0 {
		return tools.Result{Content: wrapUntrusted(b.String()), IsError: true}
	}
	return tools.Result{Content: wrapUntrusted(b.String())}
}
