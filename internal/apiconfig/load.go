package apiconfig

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Issue is one validation problem, located at file:line when known (P0-2 DoD).
type Issue struct {
	File string
	Line int // 0 = unknown
	Msg  string
}

func (i Issue) String() string {
	if i.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", i.File, i.Line, i.Msg)
	}
	return fmt.Sprintf("%s: %s", i.File, i.Msg)
}

// Issues is the error type aggregating validation failures.
type Issues []Issue

func (is Issues) Error() string {
	msgs := make([]string, len(is))
	for k, i := range is {
		msgs[k] = i.String()
	}
	return strings.Join(msgs, "\n")
}

// LoadApp reads, defaults and validates the app-global config.yaml (SPEC §18.2).
func LoadApp(path string) (*Config, error) {
	data, err := read(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := decodeStrict(path, data, cfg); err != nil {
		return nil, err
	}
	cfg.WithDefaults()
	if issues := ValidateApp(cfg, path); len(issues) > 0 {
		return cfg, Issues(issues)
	}
	return cfg, nil
}

// LoadProject reads and validates one project.yaml (SPEC §18.3).
func LoadProject(path string) (*ProjectConfig, error) {
	data, err := read(path)
	if err != nil {
		return nil, err
	}
	p := &ProjectConfig{}
	if err := decodeStrict(path, data, p); err != nil {
		return nil, err
	}
	if issues := ValidateProject(p, path); len(issues) > 0 {
		return p, Issues(issues)
	}
	return p, nil
}

// LoadSession reads and validates one session.yaml (SPEC §18.4).
func LoadSession(path string) (*SessionConfig, error) {
	data, err := read(path)
	if err != nil {
		return nil, err
	}
	s := &SessionConfig{}
	if err := decodeStrict(path, data, s); err != nil {
		return nil, err
	}
	if issues := ValidateSession(s, path); len(issues) > 0 {
		return s, Issues(issues)
	}
	return s, nil
}

func read(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return b, nil
}

// decodeStrict decodes YAML into out rejecting unknown fields (KnownFields),
// translating yaml.v3 type errors into located Issues.
func decodeStrict(path string, data []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		if err.Error() == "EOF" { // empty file → defaults
			return nil
		}
		return locate(path, err)
	}
	return nil
}

// locate extracts "line N" information from yaml.v3 error messages so callers
// can report file:line.
func locate(path string, err error) error {
	line := 0
	var inLine bool
	msg := err.Error()
	for i := 0; i+5 < len(msg); i++ {
		if msg[i:i+5] == "line " {
			j := i + 5
			for j < len(msg) && msg[j] >= '0' && msg[j] <= '9' {
				j++
			}
			if n, convErr := strconv.Atoi(msg[i+5 : j]); convErr == nil {
				line = n
				inLine = true
			}
			if inLine {
				break
			}
		}
	}
	return Issues([]Issue{{File: path, Line: line, Msg: msg}})
}

// ValidateApp checks semantic invariants beyond schema decoding (SPEC §18.5:
// `genisys config validate` lints all levels).
func ValidateApp(c *Config, file string) []Issue {
	var is []Issue
	add := func(line int, msg string) { is = append(is, Issue{File: file, Line: line, Msg: msg}) }

	if c.Version != 1 {
		add(0, fmt.Sprintf("version must be 1, got %d", c.Version))
	}
	seen := map[string]bool{}
	for _, ep := range c.Endpoints {
		if ep.ID == "" {
			add(0, "endpoint with empty id")
			continue
		}
		if seen[ep.ID] {
			add(0, fmt.Sprintf("duplicate endpoint id %q", ep.ID))
		}
		seen[ep.ID] = true
		if !strings.HasPrefix(ep.BaseURL, "http://") && !strings.HasPrefix(ep.BaseURL, "https://") {
			add(0, fmt.Sprintf("endpoint %q: base_url must start with http:// or https://, got %q", ep.ID, ep.BaseURL))
		}
		mids := map[string]bool{}
		for _, m := range ep.Models {
			if m.ID == "" {
				add(0, fmt.Sprintf("endpoint %q: model with empty id", ep.ID))
				continue
			}
			if mids[m.ID] {
				add(0, fmt.Sprintf("endpoint %q: duplicate model id %q", ep.ID, m.ID))
			}
			mids[m.ID] = true
			if bad := domainIssue("supports.tools", m.Supports.Tools); bad != "" {
				add(0, fmt.Sprintf("endpoint %q model %q: %s", ep.ID, m.ID, bad))
			}
			if bad := domainIssue("supports.reasoning", m.Supports.Reasoning); bad != "" {
				add(0, fmt.Sprintf("endpoint %q model %q: %s", ep.ID, m.ID, bad))
			}
		}
	}
	if c.DefaultModel != "" {
		if _, _, ok := c.ModelRef(c.DefaultModel); !ok {
			add(0, fmt.Sprintf("default_model %q does not reference a known endpoint/model", c.DefaultModel))
		}
	}
	if bad := domainIssue("interjection.default_mode", c.Agent.Interjection.DefaultMode); bad != "" {
		add(0, "agent.interjection: "+bad)
	}
	if bad := domainIssue("interjection.boundary", c.Agent.Interjection.Boundary); bad != "" {
		add(0, "agent.interjection: "+bad)
	}
	if bad := domainIssue("web.provider", c.Web.Provider); bad != "" {
		add(0, "web: "+bad)
	}
	if c.Web.SearchEnabled && c.Web.Provider == "searxng" {
		u := c.Web.Searxng.BaseURL
		if u == "" {
			add(0, "web: provider is searxng and search is enabled but web.searxng.base_url is empty (any reachable instance counts, see SPEC §9.5)")
		} else if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			add(0, fmt.Sprintf("web.searxng.base_url %q must start with http:// or https://", u))
		}
	}
	for _, s := range c.MCPServers {
		if bad := domainIssue("mcp.transport", s.Transport); bad != "" {
			add(0, fmt.Sprintf("mcp_server %q: %s", s.Name, bad))
		}
		if s.Transport == "stdio" && s.Command == "" {
			add(0, fmt.Sprintf("mcp_server %q: transport stdio requires command", s.Name))
		}
		if s.Transport == "http" && s.URL == "" {
			add(0, fmt.Sprintf("mcp_server %q: transport http requires url", s.Name))
		}
	}
	if bad := domainIssue("ui.theme", c.UI.Theme); bad != "" {
		add(0, "ui: "+bad)
	}
	return is
}

// ValidateProject checks one project config.
func ValidateProject(p *ProjectConfig, file string) []Issue {
	var is []Issue
	add := func(msg string) { is = append(is, Issue{File: file, Msg: msg}) }
	if p.Name == "" {
		add("name is required")
	}
	if bad := domainIssue("project.kind", p.Kind); bad != "" {
		add(bad)
	}
	if p.Kind == "" {
		add("kind is required (work|code)")
	}
	if p.WorkspacePath == "" {
		add("workspace_path is required")
	}
	if bad := domainIssue("project.persona", p.Persona); bad != "" {
		add(bad)
	}
	return is
}

// ValidateSession checks one session config.
func ValidateSession(s *SessionConfig, file string) []Issue {
	var is []Issue
	if s.ReasoningEffort != nil {
		switch *s.ReasoningEffort {
		case "low", "medium", "high":
		default:
			is = append(is, Issue{File: file, Msg: fmt.Sprintf("reasoning_effort must be low|medium|high, got %q", *s.ReasoningEffort)})
		}
	}
	return is
}

func domainIssue(key, val string) string {
	allowed, ok := domains[key]
	if !ok {
		return ""
	}
	for _, a := range allowed {
		if a == val {
			return ""
		}
	}
	return fmt.Sprintf("%s must be one of %v, got %q", key, allowed, val)
}

// ValidateAll lints config.yaml plus every project.yaml and session.yaml found
// under the workspace root (SPEC §18.5 `genisys config validate`).
func ValidateAll(cfg *Config, cfgFile string) []Issue {
	is := ValidateApp(cfg, cfgFile)
	root := ExpandPath(cfg.WorkspaceRoot)
	projects, _ := filepath.Glob(filepath.Join(root, "*", "project.yaml"))
	for _, pf := range projects {
		proj, err := LoadProject(pf)
		if issues := AsIssues(err); len(issues) > 0 {
			is = append(is, issues...)
		}
		if proj == nil {
			continue
		}
		if cfg.DefaultModel != "" && proj.Model != "" {
			if _, _, ok := cfg.ModelRef(proj.Model); !ok {
				is = append(is, Issue{File: pf, Msg: fmt.Sprintf("model %q does not reference a known endpoint/model", proj.Model)})
			}
		}
		sessions, _ := filepath.Glob(filepath.Join(filepath.Dir(pf), "sessions", "*", "session.yaml"))
		for _, sf := range sessions {
			sess, err := LoadSession(sf)
			if issues := AsIssues(err); len(issues) > 0 {
				is = append(is, issues...)
			}
			if sess != nil && sess.Model != "" {
				if _, _, ok := cfg.ModelRef(sess.Model); !ok {
					is = append(is, Issue{File: sf, Msg: fmt.Sprintf("model %q does not reference a known endpoint/model", sess.Model)})
				}
			}
		}
	}
	return is
}

// AsIssues normalizes any loader error into an Issues list (empty if nil).
func AsIssues(err error) Issues {
	if err == nil {
		return nil
	}
	if is, ok := err.(Issues); ok {
		return is
	}
	return Issues{Issue{Msg: err.Error()}}
}

// ExpandPath resolves ~ in configured paths.
func ExpandPath(p string) string {
	if p == "" {
		return ""
	}
	if p[0] == '~' {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
