// Package tools is the tool registry and dispatch (SPEC §7.1, §9). Tool
// implementations live in subpackages; the registry exposes OpenAI-shaped
// definitions and executes calls with schema-level arg validation.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/larsn03/genisys/internal/agent/events"
	"github.com/larsn03/genisys/internal/model"
	"github.com/larsn03/genisys/internal/workspace"
)

// World is everything a tool may touch (IMPL §4).
type World struct {
	Cwd          string
	Scratchpad   string
	ShellTimeout int // ms, from config agent.shell_timeout_ms
	Emit         events.Sink
	StepID       string
	Writer       *workspace.Writer // nil = P1 behavior (no grants/checkpoints)
	Perms        *workspace.Broker // nil = no permission UI
	RuntimeDir   string            // sandbox guest cache root ("" = run_python unavailable)
}

// Access resolves a tool path and enforces the grant table (SPEC §10.2).
// With no Writer wired it degrades to plain resolution (tests, P1 mode).
func (w *World) Access(path string, write bool) (string, error) {
	p := expandTilde(path)
	if w.Writer != nil {
		r, err := w.Writer.Grants().Check("", p, write)
		if err != nil {
			return "", err
		}
		return r.Path, nil
	}
	return Resolve(w.Cwd, p), nil
}

// MarkRead witnesses a file read for the read-before-write rule.
func (w *World) MarkRead(canonicalPath string) {
	if w.Writer != nil {
		w.Writer.MarkRead(canonicalPath)
	}
}

func (w *World) sink() events.Sink {
	if w.Emit != nil {
		return w.Emit
	}
	return events.Discard
}

// Result is a tool outcome. Content is already shaped (SPEC §7.3).
type Result struct {
	Content string
	IsError bool
}

// Tool is one registry entry (IMPL §4).
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Execute(ctx context.Context, args json.RawMessage, w *World) Result
}

// Registry holds tools in stable (alphabetical) definition order.
type Registry struct {
	byName map[string]Tool
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry { return &Registry{byName: map[string]Tool{}} }

// Add registers a tool; duplicate names panic (programmer error).
func (r *Registry) Add(t Tool) {
	if _, dup := r.byName[t.Name()]; dup {
		panic("tools: duplicate tool " + t.Name())
	}
	r.byName[t.Name()] = t
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) { t, ok := r.byName[name]; return t, ok }

// Names lists tool names alphabetically.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.byName))
	for n := range r.byName {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Definitions renders the OpenAI tools array.
func (r *Registry) Definitions() []model.Tool {
	out := make([]model.Tool, 0, len(r.byName))
	for _, n := range r.Names() {
		t := r.byName[n]
		out = append(out, model.Tool{
			Type: "function",
			Function: model.ToolFunction{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Schema(),
			},
		})
	}
	return out
}

// Execute dispatches one call: unknown tool → error listing valid tools
// (SPEC §23: helps XML-mode models recover); bad JSON → invalid_json.
func (r *Registry) Execute(ctx context.Context, call model.ToolCall, w *World) Result {
	t, ok := r.byName[call.Function.Name]
	if !ok {
		return Result{
			Content: fmt.Sprintf("unknown tool %q. Valid tools: %s",
				call.Function.Name, strings.Join(r.Names(), ", ")),
			IsError: true,
		}
	}
	args := call.Function.Arguments
	if strings.TrimSpace(args) == "" {
		args = "{}"
	}
	if !json.Valid([]byte(args)) {
		return Result{Content: "invalid_json: arguments are not valid JSON: " + truncate(args, 200), IsError: true}
	}
	res := t.Execute(ctx, []byte(args), w)
	res.Content = Shape(res.Content, MaxBytes, MaxLines)
	return res
}

// --- result shaping (SPEC §7.3) ---------------------------------------------

// MaxBytes/MaxLines are the §7.3 truncation budgets.
const (
	MaxBytes = 8192
	MaxLines = 2000
)

// Shape truncates tool output with an explicit marker + re-run hint.
func Shape(s string, maxBytes, maxLines int) string {
	lines := strings.Split(s, "\n")
	trunc := false
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		trunc = true
	}
	out := strings.Join(lines, "\n")
	if len(out) > maxBytes {
		// cut at a line boundary near the budget
		cut := strings.LastIndex(out[:maxBytes], "\n")
		if cut <= 0 {
			cut = maxBytes
		}
		out = out[:cut]
		trunc = true
	}
	if trunc {
		out += "\n[…truncated — re-run with narrower arguments]"
	}
	return out
}

// NoteForShell is appended to non-trivial shell output (SPEC §7.3, mirrors
// Bionic's "user does not see command output" rule).
const NoteForShell = "\n\nNote: the user cannot see this output; summarize anything relevant."

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ParseArgs is a helper for tools: decode into T with defaults pre-set.
func ParseArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	return dec.Decode(v)
}
