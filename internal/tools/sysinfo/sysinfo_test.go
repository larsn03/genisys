package sysinfo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/larsn03/genisys/internal/tools"
)

func stubWorld() *tools.World {
	return &tools.World{Gen: &tools.Genisys{
		ConfigYAML: func() (string, error) { return "agent:\n  max_steps: 24 # keep", nil },
		ConfigSet: func(path, value string) error {
			if path == "bad.key" {
				return errBoom
			}
			return nil
		},
		Reload:      func() error { return nil },
		SessionInfo: func() (map[string]any, error) { return map[string]any{"model_ref": "e/m"}, nil },
		SessionStats: func() (map[string]any, error) {
			return map[string]any{"prompt_tokens": 1234}, nil
		},
		SessionList: func() ([]map[string]any, error) {
			return []map[string]any{{"id": "s1"}}, nil
		},
		ModelSet:     func(ref string) error { return nil },
		Compact:      func() (map[string]any, error) { return map[string]any{"compacted": true}, nil },
		Checkpoints:  func() ([]map[string]any, error) { return []map[string]any{{"sha": "abc"}}, nil },
		FileHistory:  func(p string) ([]map[string]any, error) { return []map[string]any{{"sha": "a"}}, nil },
		FileRestore:  func(p, sha string, force bool) error { return nil },
		SkillsList:   func() ([]map[string]any, error) { return []map[string]any{{"name": "x"}}, nil },
		SkillInstall: func(u, c string) (string, error) { return "new-skill", nil },
		SkillRemove:  func(n string) error { return nil },
		MCPStatus:    func() []map[string]any { return []map[string]any{{"name": "srv"}} },
		WebProbe:     func() (map[string]any, error) { return map[string]any{"enabled": true}, nil },
		Models:       func() []map[string]any { return []map[string]any{{"ref": "e/m"}} },
	}}
}

var errBoom = &strErr{"nope"}

type strErr struct{ s string }

func (e *strErr) Error() string { return e.s }

func run(t *testing.T, w *tools.World, args string) tools.Result {
	t.Helper()
	res := Tool{}.Execute(context.Background(), json.RawMessage(args), w)
	if res.IsError {
		t.Logf("result: %s", res.Content)
	}
	return res
}

func TestAllActionsRespond(t *testing.T) {
	w := stubWorld()
	actions := []string{
		`{"action":"help"}`,
		`{"action":"config_get"}`,
		`{"action":"config_set","path":"agent.max_steps","value":"30"}`,
		`{"action":"config_validate"}`,
		`{"action":"session_info"}`,
		`{"action":"session_stats"}`,
		`{"action":"session_list"}`,
		`{"action":"model_set","model_ref":"e/m"}`,
		`{"action":"compact"}`,
		`{"action":"checkpoint_list"}`,
		`{"action":"file_history","path":"a.txt"}`,
		`{"action":"file_restore","path":"a.txt","sha":"abc"}`,
		`{"action":"skills_list"}`,
		`{"action":"skill_install","content":"---\nname: x\n---\n"}`,
		`{"action":"skill_remove","path":"x"}`,
		`{"action":"mcp_status"}`,
		`{"action":"web_probe"}`,
		`{"action":"models_list"}`,
	}
	for _, a := range actions {
		res := run(t, w, a)
		if res.IsError {
			t.Errorf("%s → %s", a, res.Content)
		}
		if res.Content == "" {
			t.Errorf("%s → empty", a)
		}
	}
}

func TestHelpListsEveryAction(t *testing.T) {
	res := run(t, stubWorld(), `{"action":"help"}`)
	for _, a := range []string{"config_get", "config_set", "compact", "file_restore",
		"skill_install", "mcp_status", "web_probe", "models_list", "session_stats"} {
		if !strings.Contains(res.Content, a) {
			t.Errorf("help missing %s", a)
		}
	}
}

func TestUnknownAction(t *testing.T) {
	res := run(t, stubWorld(), `{"action":"launch_missiles"}`)
	if !res.IsError || !strings.Contains(res.Content, "unknown action") {
		t.Errorf("got %q", res.Content)
	}
}

func TestConfigSetErrorSurfaces(t *testing.T) {
	res := run(t, stubWorld(), `{"action":"config_set","path":"bad.key","value":"1"}`)
	if !res.IsError || !strings.Contains(res.Content, "nope") {
		t.Errorf("got %q", res.Content)
	}
}

func TestMissingArgs(t *testing.T) {
	w := stubWorld()
	for _, a := range []string{
		`{"action":"config_set"}`,
		`{"action":"model_set"}`,
		`{"action":"file_history"}`,
		`{"action":"file_restore","path":"x"}`,
		`{"action":"skill_remove"}`,
	} {
		if res := run(t, w, a); !res.IsError {
			t.Errorf("%s should error", a)
		}
	}
}

func TestUnwiredBuild(t *testing.T) {
	res := Tool{}.Execute(context.Background(), json.RawMessage(`{"action":"compact"}`), &tools.World{})
	if !res.IsError {
		t.Errorf("nil Gen must error")
	}
}

func TestSchemaIsValid(t *testing.T) {
	var v map[string]any
	if err := json.Unmarshal(Tool{}.Schema(), &v); err != nil {
		t.Fatalf("schema invalid: %v", err)
	}
	if v["type"] != "object" {
		t.Errorf("schema type %v", v["type"])
	}
}
