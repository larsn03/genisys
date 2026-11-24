package apiconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const sampleConfig = `version: 1
workspace_root: /tmp/genisys-test/projects
default_model: dev/qwen3-coder-32b

endpoints:
  - id: dev
    name: Dev endpoint
    base_url: http://192.168.1.20:8000/v1
    api_key_env: GENISYS_DEV_KEY
    models:
      - id: qwen3-coder-32b
        context_window: 131072
        supports: { tools: native, vision: true }

agent:
  max_steps: 64
  interjection:
    default_mode: queue
    boundary: tool_batch

web:
  search_enabled: false
  provider: none

mcp_servers: []
browser: { allowed_origins: [] }
ui: { theme: dark }
`

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadAppDefaults(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "config.yaml", `
default_model: ep/m1
endpoints:
  - id: ep
    base_url: https://example.com/v1
    models: [{ id: m1 }]
`)
	cfg, err := LoadApp(p)
	if err != nil {
		t.Fatalf("LoadApp: %v", err)
	}
	if cfg.Agent.MaxSteps != 64 || cfg.Agent.ShellTimeoutMs != 10000 {
		t.Errorf("agent defaults not applied: %+v", cfg.Agent)
	}
	if cfg.Agent.Interjection.DefaultMode != "queue" || cfg.Agent.Interjection.Boundary != "tool_batch" {
		t.Errorf("interjection defaults not applied: %+v", cfg.Agent.Interjection)
	}
	if cfg.Endpoints[0].Name != "ep" {
		t.Errorf("endpoint name should default to id, got %q", cfg.Endpoints[0].Name)
	}
	m := cfg.Endpoints[0].Models[0]
	if m.ContextWindow != 8192 || m.Supports.Tools != "native" || m.Supports.Reasoning != "none" {
		t.Errorf("model defaults not applied: %+v", m)
	}
	if cfg.UI.Theme != "system" {
		t.Errorf("ui theme default: %q", cfg.UI.Theme)
	}
}

func TestValidateUnknownFieldHasLine(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "config.yaml", "version: 1\ndefault_model: a/b\nendpoints: []\nbogus_key: 1\n")
	_, err := LoadApp(p)
	issues := AsIssues(err)
	if len(issues) == 0 {
		t.Fatal("expected issues for unknown field")
	}
	is := issues[0]
	if is.File != p {
		t.Errorf("issue file = %q, want %q", is.File, p)
	}
	if is.Line != 4 {
		t.Errorf("issue line = %d, want 4 (issue: %v)", is.Line, is)
	}
	if !strings.Contains(is.String(), p+":4:") {
		t.Errorf("String() should contain file:line, got %q", is.String())
	}
}

func TestValidateSemantic(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "config.yaml", `
version: 1
default_model: ep/nope
endpoints:
  - id: ep
    base_url: ftp://bad
    models: [{ id: m1 }, { id: m1 }]
  - id: ep
    base_url: http://x
agent:
  interjection: { default_mode: teleport }
web: { search_enabled: true, provider: searxng }
ui: { theme: hotdog }
`)
	_, err := LoadApp(p)
	issues := AsIssues(err)
	want := []string{
		"duplicate endpoint id",
		"base_url must start with",
		"duplicate model id",
		"default_model",
		"interjection.default_mode",
		"searxng_url is empty",
		"ui.theme",
	}
	joined := issues.Error()
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("missing issue %q in:\n%s", w, joined)
		}
	}
}

func TestMergePrecedence(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadApp(write(t, dir, "config.yaml", sampleConfig))
	if err != nil {
		t.Fatal(err)
	}
	proj, err := LoadProject(write(t, dir, "p/project.yaml", `
name: Portfolio
kind: code
workspace_path: ~/code/portfolio
allow_coding: true
persona: coder
model: dev/qwen3-coder-32b
agent: { max_steps: 96 }
`))
	if err != nil {
		t.Fatal(err)
	}
	if proj.Agent.MaxSteps == nil || *proj.Agent.MaxSteps != 96 {
		t.Fatalf("project agent override not parsed: %+v", proj.Agent)
	}
	eff := MergeProject(cfg, proj)
	if eff.Agent.MaxSteps != 96 {
		t.Errorf("project agent override not merged: %d", eff.Agent.MaxSteps)
	}
	if eff.Agent.Interjection.DefaultMode != "queue" {
		t.Errorf("one-level-deep merge lost interjection config: %+v", eff.Agent.Interjection)
	}
	if eff.Agent.ShellTimeoutMs != 10000 {
		t.Errorf("unmentioned agent keys must survive merge")
	}

	effort := "high"
	sess := &SessionConfig{Title: "x", Model: "dev/qwen3-coder-32b", ReasoningEffort: &effort}
	if EffectiveModel(cfg, proj, sess) != "dev/qwen3-coder-32b" {
		t.Error("EffectiveModel failed")
	}
	// session > project > app
	other := &SessionConfig{Model: "ep2/m2"}
	cfg2 := *cfg
	cfg2.Endpoints = append(cfg2.Endpoints, Endpoint{ID: "ep2", BaseURL: "http://x", Models: []ModelProfile{{ID: "m2", Supports: Supports{Tools: "native", Reasoning: "none"}, ContextWindow: 8192}}})
	if EffectiveModel(&cfg2, proj, other) != "ep2/m2" {
		t.Error("session model must win")
	}
	if len(ValidateApp(&cfg2, "x")) != 0 {
		t.Error("cfg2 should validate")
	}
	if issues := ValidateSession(&SessionConfig{ReasoningEffort: strptr("ultra")}, "s.yaml"); len(issues) != 1 {
		t.Errorf("reasoning_effort enum: %v", issues)
	}
}

func strptr(s string) *string { return &s }

func TestRoundTripPreservesCommentsAndOrder(t *testing.T) {
	dir := t.TempDir()
	original := `# top comment
version: 1            # keep me
default_model: ep/m1
endpoints:            # endpoints comment
  - id: ep
    base_url: http://x/v1
agent:
  max_steps: 64
  interjection:
    default_mode: queue   # original choice
ui: { theme: system }
`
	p := write(t, dir, "config.yaml", original)

	err := Update(p, func(doc *yaml.Node) error {
		if err := Set(doc, []string{"agent", "interjection", "default_mode"}, "interrupt"); err != nil {
			return err
		}
		if err := Set(doc, []string{"agent", "max_steps"}, "96"); err != nil {
			return err
		}
		if err := Set(doc, []string{"ui", "theme"}, "dark"); err != nil {
			return err
		}
		return Set(doc, []string{"new_top_key"}, "hello")
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	got, readErr := os.ReadFile(p)
	if readErr != nil {
		t.Fatal(readErr)
	}
	s := string(got)
	for _, want := range []string{
		"# top comment",
		"# keep me",
		"# endpoints comment",
		"default_mode: interrupt",
		"max_steps: 96",
		"theme: dark",
		"new_top_key: hello",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in result:\n%s", want, s)
		}
	}
	// key order preserved: version before default_model before endpoints before agent
	idx := func(sub string) int { return strings.Index(s, sub) }
	if !(idx("version:") < idx("default_model:") && idx("default_model:") < idx("endpoints:") && idx("endpoints:") < idx("agent:")) {
		t.Errorf("key order not preserved:\n%s", s)
	}
	// the modified key itself keeps its position and its trailing comment survives
	if !strings.Contains(s, "default_mode: interrupt") {
		t.Error("value not updated")
	}
}
