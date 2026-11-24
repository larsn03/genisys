package model

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// Quirk names (SPEC §13.2). Applied automatically by Client once known.
const (
	QuirkDropMaxTokens        = "drop_max_tokens"
	QuirkRejectsStreamOptions = "rejects_stream_options"
	QuirkRejectsTools         = "rejects_tools"
	QuirkRequiresToolCallIDs  = "requires_tool_call_ids"
	QuirkEmitsThinkTags       = "emits_think_tags"
)

// ProbeResult is the outcome of the "Test connection" capability probe
// (SPEC §13.2: tiny tool-call probe round-trip; user-overridable afterwards).
type ProbeResult struct {
	Tools  string   // native | xml | none
	Usage  bool     // stream_options.include_usage honored
	Quirks []string // detected wire-level quirks
	Notes  []string // human-readable observations for the settings UI
}

// probeTool is the throwaway function the model is asked to call.
var probeTool = []Tool{{
	Type: "function",
	Function: ToolFunction{
		Name:        "probe_echo",
		Description: "Call this tool immediately with text set to ping. Do not answer in prose.",
		Parameters:  jsonRaw(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
	},
}}

// Probe classifies how this endpoint/model handles tools, usage chunks and
// sampling params. Each parameter is probed independently so multiple quirks
// are discovered in one run. Vision and reasoning support are configuration
// concerns (they cannot be probed cheaply) and stay at their configured values.
func (c *Client) Probe(ctx context.Context, modelID string) (*ProbeResult, error) {
	res := &ProbeResult{Tools: "none"}
	pol := &RetryPolicy{MaxAttempts: 2, Base: 50 * time.Millisecond, MaxSleep: 200 * time.Millisecond}
	probe := func(withTools, withMaxTokens bool) (*Completion, error) {
		req := ChatRequest{
			Model:    modelID,
			Messages: []Message{{Role: "user", Content: "Call the probe_echo tool now with text=\"ping\"."}},
		}
		if withTools {
			req.Tools = probeTool
			req.ToolChoice = "auto"
		}
		if withMaxTokens {
			mt := 128
			req.MaxTokens = &mt
		}
		return c.CompleteWith(ctx, req, pol)
	}
	observe := func(comp *Completion) {
		if comp == nil {
			return
		}
		if comp.Usage.PromptTokens > 0 || comp.Usage.TotalTokens > 0 {
			res.Usage = true
		}
		if comp.Reasoning != "" {
			res.Notes = append(res.Notes, "model emits a separate reasoning channel")
		}
		if thinkTagRe.MatchString(comp.Text) {
			res.Quirks = append(res.Quirks, QuirkEmitsThinkTags)
		}
	}

	// 0) plain request must work at all (auth/connectivity failure → abort).
	plain, err := probe(false, false)
	if err != nil {
		return nil, err
	}
	observe(plain)

	// 1) max_tokens support.
	mtOK := true
	compMT, err := probe(false, true)
	switch {
	case err == nil:
		observe(compMT)
	case isRequestRejection(err):
		mtOK = false
		res.Quirks = append(res.Quirks, QuirkDropMaxTokens)
		res.Notes = append(res.Notes, "endpoint rejects max_tokens; omitting it")
	default:
		return nil, err
	}

	// 2) tools support + tool-call style.
	compT, err := probe(true, mtOK)
	switch {
	case err == nil:
		observe(compT)
		switch {
		case compT.HasToolCalls() && compT.ToolCalls[0].Function.Name == "probe_echo":
			res.Tools = "native"
		case containsXMLCall(compT.Text, "probe_echo"):
			res.Tools = "xml"
			res.Notes = append(res.Notes, "model emitted an XML-style tool call in native mode; XML fallback available")
		default:
			res.Notes = append(res.Notes, "tools accepted but no call observed; XML prompt mode recommended")
		}
	case isRequestRejection(err):
		res.Quirks = append(res.Quirks, QuirkRejectsTools)
		res.Notes = append(res.Notes, "endpoint rejects the tools field; XML mode required")
	default:
		return nil, err
	}

	if !res.Usage {
		res.Quirks = append(res.Quirks, QuirkRejectsStreamOptions)
		res.Notes = append(res.Notes, "no usage chunk received; token meter will use estimates")
	}
	return res, nil
}

// WithQuirks makes the client apply the given wire-level quirks to requests.
func WithQuirks(quirks ...string) Option {
	return func(c *Client) {
		for _, q := range quirks {
			c.quirks[q] = true
		}
	}
}

var thinkTagRe = regexp.MustCompile(`(?s)<think\b.*?</think>|<thinking\b.*?</thinking>`)

// StripThinkTags removes  … blocks some reasoning
// models leak into content (SPEC §13.2 quirk emits_think_tags).
func StripThinkTags(s string) string {
	return strings.TrimSpace(thinkTagRe.ReplaceAllString(s, ""))
}

// containsXMLCall reports whether text carries <name>…</name> call syntax.
func containsXMLCall(text, name string) bool {
	re := regexp.MustCompile(`(?s)<(?:antml:)?` + regexp.QuoteMeta(name) + `\b[^>]*>.*?</(?:antml:)?` + regexp.QuoteMeta(name) + `>`)
	return re.MatchString(text)
}

func isRequestRejection(err error) bool {
	ae, ok := AsAPIError(err)
	return ok && ae.Kind == KindRequest
}

func jsonRaw(s string) json.RawMessage { return json.RawMessage(s) }
