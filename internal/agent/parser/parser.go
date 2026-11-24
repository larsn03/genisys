// Package parser is the open-model robustness layer of SPEC §13.4: it turns
// whatever a model produced — native tool_calls deltas or XML-style calls in
// prose — into OpenAI-shaped tool calls. It is dependency-light on purpose:
// it is the fuzzing target of IMPL §13.
package parser

import (
	"encoding/json"
	"strings"

	"github.com/larsn03/genisys/internal/model"
)

// FromNative assembles tool calls from native SSE deltas (order by index,
// synthesized ids for missing ones — handled by the model accumulator).
func FromNative(deltas []model.ToolCallDelta) []model.ToolCall {
	acc := make([]*model.ToolCall, 0, len(deltas))
	byIdx := map[int]*model.ToolCall{}
	var order []int
	for i := range deltas {
		d := deltas[i]
		tc, ok := byIdx[d.Index]
		if !ok {
			tc = &model.ToolCall{Type: "function"}
			byIdx[d.Index] = tc
			order = append(order, d.Index)
		}
		if d.ID != "" {
			tc.ID = d.ID
		}
		if d.Type != "" {
			tc.Type = d.Type
		}
		if d.Name != "" {
			tc.Function.Name = d.Name
		}
		tc.Function.Arguments += d.Arguments
	}
	for i, idx := range order {
		tc := byIdx[idx]
		if tc.ID == "" {
			tc.ID = model.SynthesizeID(i)
		}
		if tc.Type == "" {
			tc.Type = "function"
		}
		acc = append(acc, tc)
	}
	out := make([]model.ToolCall, len(acc))
	for i, tc := range acc {
		out[i] = *tc
	}
	return out
}

// ParseText extracts XML-style tool calls from assistant prose:
//
//	<tool_name>{"arg": "value"}</tool_name>
//	<antml:invoke name="tool_name"><antml:parameter name="arg">v</…></antml:invoke>
//
// Tolerated (SPEC §13.4): markdown fences around calls, single quotes,
// trailing commas, truncated JSON, missing closing tags, antml namespaces.
// A tag only counts as a call when its body starts with '{' (or is empty or
// antml), so ordinary markup like <div>text</div> stays prose.
//
// Returns the prose with call spans removed, the calls (ids tc_1.. synthesized)
// and per-call repair errors (a call with unrepairable JSON is still
// returned — the loop feeds invalid_json back to the model).
func ParseText(text string) (prose string, calls []model.ToolCall, errs []string) {
	rest := text
	var out []model.ToolCall
	var outErrs []string
	offset := 0

	for {
		rel := findCallOpen(rest[offset:])
		if rel == nil {
			break
		}
		open := *rel
		open.start += offset
		open.end += offset
		name := rest[open.nameStart:open.nameEnd]
		if open.antml {
			name = "antml:invoke"
		}
		bodyStart := open.end
		var bodyEnd, consumeEnd int
		closeTag := "</" + name + ">"
		if idx := strings.Index(rest[bodyStart:], closeTag); idx >= 0 {
			bodyEnd = bodyStart + idx
			consumeEnd = bodyEnd + len(closeTag)
		} else {
			// missing closing tag: body runs to the next opening tag or EOF
			bodyEnd = len(rest)
			if next := findCallOpen(rest[bodyStart:]); next != nil {
				bodyEnd = bodyStart + next.start
			}
			consumeEnd = bodyEnd
		}
		body := strings.TrimSpace(rest[bodyStart:bodyEnd])

		// A tag is only a call when the body is JSON (or fenced JSON, or empty,
		// or antml); ordinary markup (<div>x</div>) stays prose.
		if !open.antml && body != "" && !strings.HasPrefix(body, "{") && !strings.HasPrefix(body, "```") {
			offset = open.start + 1
			continue
		}

		tc := model.ToolCall{ID: model.SynthesizeID(len(out)), Type: "function"}
		tc.Function.Name = name
		if open.antml {
			if j := strings.Index(open.raw(rest), `name="`); j >= 0 {
				if k := strings.Index(open.raw(rest)[j+6:], `"`); k >= 0 {
					tc.Function.Name = open.raw(rest)[j+6 : j+6+k]
				}
			}
		}

		if strings.HasPrefix(name, "antml:invoke") || open.antml {
			// antml form: parameters are children, rebuild the JSON object
			args, perr := antmlArgs(body)
			tc.Function.Arguments = args
			if perr != "" {
				outErrs = append(outErrs, perr)
			}
		} else if body == "" {
			tc.Function.Arguments = "{}"
		} else {
			repaired, err := RepairJSON(body)
			if err != nil {
				outErrs = append(outErrs, name+": "+err.Error())
				tc.Function.Arguments = body // hand raw to the loop → invalid_json result
			} else {
				tc.Function.Arguments = string(repaired)
			}
		}
		out = append(out, tc)
		rest = rest[:open.start] + rest[consumeEnd:] // remove span (prose kept around it)
		offset = open.start
	}

	prose = strings.TrimSpace(collapseBlankLines(rest))
	return prose, out, outErrs
}

// collapseBlankLines removes the gaps left by removed call spans.
func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	var keep []string
	blank := false
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			blank = true
			continue
		}
		if blank && len(keep) > 0 {
			keep = append(keep, "")
		}
		blank = false
		keep = append(keep, l)
	}
	return strings.Join(keep, "\n")
}

type openSpan struct {
	start, end         int // full open tag span
	nameStart, nameEnd int
	antml              bool
}

// raw returns the open tag text (for antml name="…" extraction).
func (o *openSpan) raw(s string) string { return s[o.start:o.end] }

// findCallOpen finds the next plausible call opening tag.
func findCallOpen(s string) *openSpan {
	for i := 0; i < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		// antml:invoke name="x"
		if strings.HasPrefix(s[i:], "<antml:invoke ") {
			// require a name attribute
			if j := strings.Index(s[i:], `name="`); j >= 0 && j < 64 {
				quoteEnd := strings.Index(s[i+j+6:], `"`)
				if quoteEnd >= 0 {
					tagEnd := strings.Index(s[i:], ">")
					if tagEnd >= 0 {
						return &openSpan{start: i, end: i + tagEnd + 1,
							nameStart: i, nameEnd: i, antml: true}
					}
				}
			}
		}
		// plain <tool_name ...> with a tool-shaped name (≥3 chars, lowercase)
		j := i + 1
		for j < len(s) && (s[j] >= 'a' && s[j] <= 'z' || s[j] >= '0' && s[j] <= '9' || s[j] == '_') {
			j++
		}
		nameLen := j - (i + 1)
		if nameLen < 3 || nameLen > 40 {
			continue
		}
		if j < len(s) && (s[j] == '>' || s[j] == ' ') {
			tagEnd := strings.Index(s[j:], ">")
			if tagEnd < 0 {
				continue
			}
			return &openSpan{start: i, end: j + tagEnd + 1, nameStart: i + 1, nameEnd: j}
		}
	}
	return nil
}

// antmlArgs rebuilds a JSON object from <antml:parameter name="k">v</…> children.
func antmlArgs(body string) (string, string) {
	var errs []string
	obj := map[string]string{}
	rest := body
	for {
		i := strings.Index(rest, `<antml:parameter name="`)
		if i < 0 {
			break
		}
		rest = rest[i+len(`<antml:parameter name="`):]
		j := strings.Index(rest, `"`)
		if j < 0 {
			errs = append(errs, "antml: unterminated parameter name")
			break
		}
		key := rest[:j]
		rest = rest[j+1:]
		if g := strings.Index(rest, ">"); g >= 0 && g < 3 { // skip open-tag remainder ">"
			rest = rest[g+1:]
		}
		k := strings.Index(rest, "</antml:parameter>")
		if k < 0 {
			obj[key] = strings.TrimSpace(rest) // truncated last parameter
			errs = append(errs, "antml: missing parameter close for "+key)
			break
		}
		obj[key] = strings.TrimSpace(rest[:k])
		rest = rest[k+len("</antml:parameter>"):]
	}
	if len(obj) == 0 {
		// maybe the body was plain JSON after all
		if repaired, err := RepairJSON(body); err == nil {
			return string(repaired), ""
		}
		return "{}", "antml: no parameters found"
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return "{}", "antml: marshal: " + err.Error()
	}
	return string(b), strings.Join(errs, "; ")
}
