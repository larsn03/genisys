// subagent.go — the agentic_find locator sub-agent (SPEC §12): fresh
// context, locator-only prompt, 4-tool restriction, budgets (≤12 steps,
// ≤90 s, ≤4 KB), nested trace events, failure isolation per query.

package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/larsn03/genisys/internal/agent/events"
	"github.com/larsn03/genisys/internal/tools"
	"github.com/larsn03/genisys/internal/tools/fileio"
	"github.com/larsn03/genisys/internal/tools/search"
)

//go:embed templates/locator.tmpl
var locatorTmplText string

var locatorTmpl = template.Must(template.New("locator").Parse(locatorTmplText))

// Sub-agent budgets (SPEC §12).
const (
	subagentMaxSteps = 12
	subagentTimeout  = 90 * time.Second
	subagentMaxOut   = 4096
)

// RenderLocator renders the locator-only system prompt.
func RenderLocator(cwd string) (string, error) {
	var b strings.Builder
	if err := locatorTmpl.Execute(&b, struct{ Cwd string }{cwd}); err != nil {
		return "", err
	}
	return b.String(), nil
}

// subagentTools is the normative 4-tool set (SPEC §9.4/§12).
func subagentTools() *tools.Registry {
	r := tools.NewRegistry()
	r.Add(search.SearchLine{})
	r.Add(search.FindFiles{})
	r.Add(fileio.ListDir{})
	r.Add(fileio.ReadLines{})
	return r
}

// SubagentQuery runs one locating task against the parent's endpoint and
// returns the findings (never an error — failures come back as an
// "[query failed: …]" string so one bad query cannot sink the batch).
// stepID attributes the nested trace under the parent tool chip.
func SubagentQuery(ctx context.Context, parent Deps, cwd, target, stepID string) string {
	if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(parent.Cwd, cwd)
	}
	sys, err := RenderLocator(cwd)
	if err != nil {
		return "[query failed: " + err.Error() + "]"
	}
	modelRef := parent.Model
	if parent.SubagentModel != "" {
		modelRef = parent.SubagentModel
	}
	deps := Deps{
		Client: parent.Client, Model: modelRef,
		Tools: subagentTools(), ToolsMode: parent.ToolsMode,
		MaxSteps: subagentMaxSteps,
		Cwd:      cwd, Scratchpad: parent.Scratchpad,
		Retry:          parent.Retry,
		ShellTimeout:   parent.ShellTimeout,
		Writer:         parent.Writer, // grants apply to the locator too
		SystemOverride: sys,
		Events:         nestedSink(parent.Events, stepID),
	}
	sess := NewSession(deps)
	ctx, cancel := context.WithTimeout(ctx, subagentTimeout)
	defer cancel()
	out, err := sess.Run(ctx, "Locate: "+target)
	if err != nil && out == "" {
		return "[query failed: " + err.Error() + "]"
	}
	if len(out) > subagentMaxOut {
		out = out[:subagentMaxOut] + "\n…[truncated at 4 KB]"
	}
	return out
}

// nestedSink re-emits sub-agent events tagged for the UI to render as a
// collapsed trace under the parent step (SPEC §12).
func nestedSink(sink events.Sink, stepID string) events.Sink {
	if sink == nil {
		return events.Discard
	}
	return events.SinkFunc(func(e events.Event) {
		pl := map[string]any{}
		switch p := e.Payload.(type) {
		case map[string]any:
			for k, v := range p {
				pl[k] = v
			}
		case nil:
		default:
			if b, err := json.Marshal(p); err == nil {
				_ = json.Unmarshal(b, &pl)
			}
		}
		pl["parent_step"] = stepID
		pl["subagent"] = true
		sink.Emit(events.Event{Type: e.Type, Payload: pl})
	})
}
