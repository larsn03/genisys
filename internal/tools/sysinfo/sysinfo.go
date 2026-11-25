// Package sysinfo implements genisys_tool (SPEC §9.8): the agent's
// introspection surface — config get/set/validate, session info/stats,
// checkpoints, skills, MCP status, web probe.
package sysinfo

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/larsn03/genisys/internal/tools"
)

//go:embed schema.json
var schemaJSON []byte

// Genisys is the façade App wires into World; every action maps to one
// function so the tool never touches storage layers directly.
type Genisys struct {
	ConfigYAML   func() (string, error)
	ConfigSet    func(path, value string) error // dot-path, comment-preserving
	Reload       func() error
	SessionInfo  func() (map[string]any, error)
	SessionStats func() (map[string]any, error)
	SessionList  func() ([]map[string]any, error)
	ModelSet     func(ref string) error
	Compact      func() (map[string]any, error)
	Checkpoints  func() ([]map[string]any, error)
	FileHistory  func(path string) ([]map[string]any, error)
	FileRestore  func(path, sha string, force bool) error
	SkillsList   func() ([]map[string]any, error)
	SkillInstall func(url, content string) (string, error)
	SkillRemove  func(name string) error
	MCPStatus    func() []map[string]any
	WebProbe     func() (map[string]any, error)
	Models       func() []map[string]any
}

type Tool struct{}

func (Tool) Name() string { return "genisys_tool" }

func (Tool) Description() string {
	return "Inspect and manage Genisys itself: config, session stats, checkpoints, skills, MCP, web. Call action=help first."
}

func (Tool) Schema() json.RawMessage { return schemaJSON }

func (t Tool) Execute(ctx context.Context, args json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Action string `json:"action"`
		Path   string `json:"path"`
		Value  string `json:"value"`
		SHA    string `json:"sha"`
		Force  bool   `json:"force"`
		Title  string `json:"title"`
		Ref    string `json:"model_ref"`
		URL    string `json:"url"`
		Text   string `json:"content"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return tools.Result{Content: "invalid_json: " + err.Error(), IsError: true}
	}
	g := w.Gen
	if g == nil {
		return tools.Result{Content: "genisys_tool is not wired in this build", IsError: true}
	}

	switch a.Action {
	case "help", "":
		return ok(strings.Join(helpLines, "\n"))

	case "config_get":
		if g.ConfigYAML == nil {
			return errResult("unavailable")
		}
		s, err := g.ConfigYAML()
		if err != nil {
			return errResult(err.Error())
		}
		return ok(s)

	case "config_set":
		if g.ConfigSet == nil || a.Path == "" {
			return errResult("path is required")
		}
		if err := g.ConfigSet(a.Path, a.Value); err != nil {
			return errResult(err.Error())
		}
		return ok(fmt.Sprintf("set %s = %s (config.yaml updated, comments preserved)", a.Path, a.Value))

	case "config_validate":
		if g.Reload == nil {
			return errResult("unavailable")
		}
		if err := g.Reload(); err != nil {
			return errResult(err.Error())
		}
		return ok("config.yaml is valid and hot-reloaded")

	case "session_info":
		m, err := call(g.SessionInfo)
		if err != nil {
			return errResult(err.Error())
		}
		return jsonResult(m)

	case "session_stats":
		m, err := call(g.SessionStats)
		if err != nil {
			return errResult(err.Error())
		}
		return jsonResult(m)

	case "session_list":
		if g.SessionList == nil {
			return errResult("unavailable")
		}
		m, err := g.SessionList()
		if err != nil {
			return errResult(err.Error())
		}
		return jsonResult(m)

	case "model_set":
		if g.ModelSet == nil || a.Ref == "" {
			return errResult("model_ref is required")
		}
		if err := g.ModelSet(a.Ref); err != nil {
			return errResult(err.Error())
		}
		return ok("session model set to " + a.Ref)

	case "compact":
		m, err := call(g.Compact)
		if err != nil {
			return errResult(err.Error())
		}
		return jsonResult(m)

	case "checkpoint_list":
		if g.Checkpoints == nil {
			return errResult("unavailable")
		}
		m, err := g.Checkpoints()
		if err != nil {
			return errResult(err.Error())
		}
		return jsonResult(m)

	case "file_history":
		if g.FileHistory == nil || a.Path == "" {
			return errResult("path is required")
		}
		m, err := g.FileHistory(a.Path)
		if err != nil {
			return errResult(err.Error())
		}
		return jsonResult(m)

	case "file_restore":
		if g.FileRestore == nil || a.Path == "" || a.SHA == "" {
			return errResult("path and sha are required")
		}
		if err := g.FileRestore(a.Path, a.SHA, a.Force); err != nil {
			return errResult(err.Error())
		}
		return ok(fmt.Sprintf("restored %s to %s", a.Path, short(a.SHA)))

	case "skills_list":
		if g.SkillsList == nil {
			return errResult("unavailable")
		}
		m, err := g.SkillsList()
		if err != nil {
			return errResult(err.Error())
		}
		return jsonResult(m)

	case "skill_install":
		if g.SkillInstall == nil {
			return errResult("unavailable")
		}
		name, err := g.SkillInstall(a.URL, a.Text)
		if err != nil {
			return errResult(err.Error())
		}
		return ok("installed skill " + name)

	case "skill_remove":
		if g.SkillRemove == nil || a.Path == "" {
			return errResult("path (skill name) is required")
		}
		if err := g.SkillRemove(a.Path); err != nil {
			return errResult(err.Error())
		}
		return ok("removed skill " + a.Path)

	case "mcp_status":
		if g.MCPStatus == nil {
			return ok("[]")
		}
		return jsonResult(g.MCPStatus())

	case "web_probe":
		m, err := call(g.WebProbe)
		if err != nil {
			return errResult(err.Error())
		}
		return jsonResult(m)

	case "models_list":
		if g.Models == nil {
			return errResult("unavailable")
		}
		return jsonResult(g.Models())

	default:
		return errResult("unknown action " + a.Action + " — call action=\"help\" for the list")
	}
}

func call(f func() (map[string]any, error)) (map[string]any, error) {
	if f == nil {
		return nil, fmt.Errorf("unavailable")
	}
	return f()
}

func jsonResult(v any) tools.Result {
	if v == nil {
		v = []any{}
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errResult(err.Error())
	}
	return ok(string(b))
}

func ok(s string) tools.Result { return tools.Result{Content: s} }

func errResult(s string) tools.Result {
	return tools.Result{Content: "error: " + s, IsError: true}
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

var helpLines = []string{
	`genisys_tool actions (SPEC §9.8):
  help                          this list
  config_get                    current config.yaml (YAML, comments intact)
  config_set {path,value}       set one key, e.g. path="agent.max_retries" value="5"
  config_validate               re-parse config.yaml (reports issues)
  session_info                  current session, project, model, status
  session_stats                 token + step + tool-call totals for this session
  session_list                  recent sessions (id, title, model, status)
  model_set {model_ref}         switch this session's model ("endpoint/model")
  compact                       summarize+prune context now (frees window)
  checkpoint_list               recent checkpoints (sha, step, label)
  file_history {path}           snapshot timeline for one workspace file
  file_restore {path,sha,force} roll one file back (force=true overrides user edits)
  skills_list                   discovered skills
  skill_install {url|content}   install a SKILL.md (raw url or inline content)
  skill_remove {path}           remove a user skill (name as path)
  mcp_status                    configured MCP servers + tool counts
  web_probe                     test the configured web search backend
  models_list                   models available from configured endpoints`,
}

// sortKeys is used by tests for deterministic comparison.
func sortKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
