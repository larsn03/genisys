// Package apiconfig is the single source of truth for ALL Genisys configuration,
// which per SPEC §18 is exclusively YAML. It loads the three precedence levels
// (config.yaml → project.yaml → session.yaml), applies defaults, validates with
// file:line diagnostics, and can write values back comment-preservingly.
package apiconfig

// Config is the app-global configuration (~/.genisys/config.yaml, SPEC §18.2).
type Config struct {
	Version       int         `yaml:"version"`
	WorkspaceRoot string      `yaml:"workspace_root"`
	DefaultModel  string      `yaml:"default_model"`
	Endpoints     []Endpoint  `yaml:"endpoints"`
	Agent         AgentConfig `yaml:"agent"`
	Web           WebConfig   `yaml:"web"`
	MCPServers    []MCPServer `yaml:"mcp_servers"`
	Browser       Browser     `yaml:"browser"`
	Skills        Skills      `yaml:"skills"`
	UI            UIConfig    `yaml:"ui"`
}

// Endpoint is one OpenAI-compatible server (SPEC §18.2).
type Endpoint struct {
	ID        string         `yaml:"id"`
	Name      string         `yaml:"name"`
	BaseURL   string         `yaml:"base_url"`
	APIKeyEnv string         `yaml:"api_key_env"`
	APIKey    string         `yaml:"api_key"` // discouraged; literal secret
	Models    []ModelProfile `yaml:"models"`
}

// ModelProfile carries per-model capability flags (SPEC §13.2).
type ModelProfile struct {
	ID            string   `yaml:"id"`
	ContextWindow int      `yaml:"context_window"`
	Supports      Supports `yaml:"supports"`
	Sampling      Sampling `yaml:"sampling"`
	Quirks        []string `yaml:"quirks"`
}

// Supports enumerates capabilities: tools native|xml|none, reasoning effort|none.
type Supports struct {
	Tools     string `yaml:"tools"`
	Vision    bool   `yaml:"vision"`
	Reasoning string `yaml:"reasoning"`
}

// Sampling holds optional sampling parameters; nil means "omit from request".
type Sampling struct {
	Temperature *float64 `yaml:"temperature"`
	TopP        *float64 `yaml:"top_p"`
}

// AgentConfig maps to SPEC §18.2 agent block.
type AgentConfig struct {
	MaxSteps       int                `yaml:"max_steps"`
	SubagentModel  *string            `yaml:"subagent_model"`
	ShellTimeoutMs int                `yaml:"shell_timeout_ms"`
	Interjection   InterjectionConfig `yaml:"interjection"`
}

// InterjectionConfig maps to SPEC §7.4 / §18.2 agent.interjection.
type InterjectionConfig struct {
	DefaultMode      string `yaml:"default_mode"`       // queue | interrupt
	ModifierToInvert string `yaml:"modifier_to_invert"` // shift | ctrl | alt | none
	Boundary         string `yaml:"boundary"`           // tool_batch | step_end
}

// WebConfig maps to SPEC §18.2 web block.
type WebConfig struct {
	SearchEnabled bool          `yaml:"search_enabled"`
	Provider      string        `yaml:"provider"` // searxng | tavily | brave | none
	Searxng       SearxngConfig `yaml:"searxng"`
	Tavily        KeyConfig     `yaml:"tavily"`
	Brave         KeyConfig     `yaml:"brave"`
}

// KeyConfig parameterizes the optional commercial providers (§16).
type KeyConfig struct {
	APIKeyEnv string `yaml:"api_key_env"`
	BaseURL   string `yaml:"base_url"` // optional override (testing)
}

// SearxngConfig configures the default search backend: any reachable
// SearXNG instance, local or remote (SPEC §9.5).
type SearxngConfig struct {
	BaseURL          string `yaml:"base_url"`    // required when provider=searxng
	Language         string `yaml:"language"`    // "auto"/"" = instance default
	Categories       string `yaml:"categories"`  // instance syntax
	TimeoutMs        int    `yaml:"timeout_ms"`  // default 10000
	APIKeyEnv        string `yaml:"api_key_env"` // optional bearer token env var
	AllowInsecureTLS bool   `yaml:"allow_insecure_tls"`
}

// MCPServer maps to SPEC §15 / §18.2 mcp_servers entries.
type MCPServer struct {
	Name      string            `yaml:"name"`
	Command   string            `yaml:"command"`
	Args      []string          `yaml:"args"`
	Env       map[string]string `yaml:"env"`
	URL       string            `yaml:"url"`
	Transport string            `yaml:"transport"` // stdio | http
	Enabled   bool              `yaml:"enabled"`
}

// Browser maps to SPEC §18.2 browser block.
type Browser struct {
	AllowedOrigins []string `yaml:"allowed_origins"`
}

// Skills maps to SPEC §18.2 skills block.
type Skills struct {
	ImportFromOtherApps []string `yaml:"import_from_other_apps"`
}

// UIConfig maps to SPEC §18.2 ui block.
type UIConfig struct {
	Theme string `yaml:"theme"` // light | dark | system
}

// ProjectConfig is one project.yaml (SPEC §18.3).
type ProjectConfig struct {
	Name          string         `yaml:"name"`
	Kind          string         `yaml:"kind"` // work | code
	WorkspacePath string         `yaml:"workspace_path"`
	AllowCoding   bool           `yaml:"allow_coding"`
	Persona       string         `yaml:"persona"` // coder | researcher
	Model         string         `yaml:"model"`
	Agent         *AgentOverride `yaml:"agent"`
}

// AgentOverride is a partial AgentConfig; only set keys override (SPEC §18.1:
// agent.* sub-keys merge one level deep).
type AgentOverride struct {
	MaxSteps       *int    `yaml:"max_steps"`
	SubagentModel  *string `yaml:"subagent_model"`
	ShellTimeoutMs *int    `yaml:"shell_timeout_ms"`
}

// SessionConfig is one session.yaml (SPEC §18.4).
type SessionConfig struct {
	Title           string  `yaml:"title"`
	Model           string  `yaml:"model"`
	ReasoningEffort *string `yaml:"reasoning_effort"`
	Pinned          bool    `yaml:"pinned"`
	Archived        bool    `yaml:"archived"`
}

// Enum domains used by validation.
var (
	domains = map[string][]string{
		"supports.tools":            {"native", "xml", "none"},
		"supports.reasoning":        {"", "effort", "none"},
		"interjection.default_mode": {"queue", "interrupt"},
		"interjection.boundary":     {"tool_batch", "step_end"},
		"web.provider":              {"searxng", "tavily", "brave", "none"},
		"mcp.transport":             {"stdio", "http"},
		"ui.theme":                  {"light", "dark", "system"},
		"project.kind":              {"work", "code"},
		"project.persona":           {"coder", "researcher"},
	}
)

const (
	defaultWorkspaceRoot = "~/.genisys/projects"
	defaultModelCtxWin   = 8192
)

// WithDefaults returns a copy of c with all defaults applied (SPEC §18.2).
// It mutates and returns the same pointer for chaining convenience.
func (c *Config) WithDefaults() *Config {
	if c.Version == 0 {
		c.Version = 1
	}
	if c.WorkspaceRoot == "" {
		c.WorkspaceRoot = defaultWorkspaceRoot
	}
	if c.Agent.MaxSteps == 0 {
		c.Agent.MaxSteps = 64
	}
	if c.Agent.ShellTimeoutMs == 0 {
		c.Agent.ShellTimeoutMs = 10000
	}
	ij := &c.Agent.Interjection
	if ij.DefaultMode == "" {
		ij.DefaultMode = "queue"
	}
	if ij.ModifierToInvert == "" {
		ij.ModifierToInvert = "shift"
	}
	if ij.Boundary == "" {
		ij.Boundary = "tool_batch"
	}
	if c.Web.Provider == "" {
		if c.Web.SearchEnabled {
			c.Web.Provider = "searxng"
		} else {
			c.Web.Provider = "none"
		}
	}
	if c.Web.Searxng.TimeoutMs == 0 {
		c.Web.Searxng.TimeoutMs = 10000
	}
	if c.Web.Searxng.Language == "" {
		c.Web.Searxng.Language = "auto"
	}
	if c.UI.Theme == "" {
		c.UI.Theme = "system"
	}
	for i := range c.Endpoints {
		ep := &c.Endpoints[i]
		if ep.Name == "" {
			ep.Name = ep.ID
		}
		for j := range ep.Models {
			m := &ep.Models[j]
			if m.ContextWindow == 0 {
				m.ContextWindow = defaultModelCtxWin
			}
			if m.Supports.Tools == "" {
				m.Supports.Tools = "native"
			}
			if m.Supports.Reasoning == "" {
				m.Supports.Reasoning = "none"
			}
		}
	}
	return c
}

// ModelRef resolves "endpoint/model" against the config; ok=false when unknown.
func (c *Config) ModelRef(ref string) (*Endpoint, *ModelProfile, bool) {
	epID, modelID := splitRef(ref)
	for i := range c.Endpoints {
		ep := &c.Endpoints[i]
		if ep.ID != epID {
			continue
		}
		for j := range ep.Models {
			if ep.Models[j].ID == modelID {
				return ep, &ep.Models[j], true
			}
		}
	}
	return nil, nil, false
}

// EffectiveModel applies SPEC §18.1 precedence: session > project > app default.
func EffectiveModel(cfg *Config, proj *ProjectConfig, sess *SessionConfig) string {
	if sess != nil && sess.Model != "" {
		return sess.Model
	}
	if proj != nil && proj.Model != "" {
		return proj.Model
	}
	return cfg.DefaultModel
}

// MergeProject returns the app config with project-level overrides applied
// (shallow per-key; agent.* one level deep). The returned Config is a copy.
func MergeProject(cfg *Config, proj *ProjectConfig) *Config {
	out := *cfg
	if proj == nil {
		return &out
	}
	if proj.Model != "" {
		out.DefaultModel = proj.Model
	}
	if a := proj.Agent; a != nil {
		if a.MaxSteps != nil {
			out.Agent.MaxSteps = *a.MaxSteps
		}
		if a.SubagentModel != nil {
			v := *a.SubagentModel
			out.Agent.SubagentModel = &v
		}
		if a.ShellTimeoutMs != nil {
			out.Agent.ShellTimeoutMs = *a.ShellTimeoutMs
		}
	}
	return &out
}

// MergeSession applies session-level overrides on top of an already
// project-merged config.
func MergeSession(cfg *Config, sess *SessionConfig) *Config {
	out := *cfg
	if sess != nil && sess.Model != "" {
		out.DefaultModel = sess.Model
	}
	return &out
}

func splitRef(ref string) (endpointID, modelID string) {
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == '/' {
			return ref[:i], ref[i+1:]
		}
	}
	return ref, ""
}
