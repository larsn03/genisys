// manager.go — lazy lifecycle (SPEC §15): a server starts on the first
// get_mcp_info in a session and idles out after 5 minutes.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ServerConfig mirrors apiconfig.MCPServer (kept decoupled).
type ServerConfig struct {
	Name      string
	Command   string
	Args      []string
	Env       map[string]string
	URL       string
	Transport string // stdio | http
}

// ServerInfo is the get_mcp_info row (names only — SPEC §15).
type ServerInfo struct {
	Name   string   `json:"name"`
	Online bool     `json:"online"`
	Error  string   `json:"error,omitempty"`
	Tools  []string `json:"tools"`
}

// IdleTimeout is the SPEC §15 default.
const IdleTimeout = 5 * time.Minute

type conn struct {
	c       *client
	tools   []ToolDef
	lastUse time.Time
	mu      sync.Mutex
}

// Manager owns the enabled servers and their lazy connections.
type Manager struct {
	mu    sync.Mutex
	cfgs  map[string]ServerConfig
	conns map[string]*conn
	idle  time.Duration
}

// NewManager takes the enabled server configs.
func NewManager(cfgs []ServerConfig) *Manager {
	m := &Manager{cfgs: map[string]ServerConfig{}, conns: map[string]*conn{}, idle: IdleTimeout}
	for _, c := range cfgs {
		if c.Name != "" {
			m.cfgs[c.Name] = c
		}
	}
	return m
}

// Empty reports whether there is nothing to register the tools for.
func (m *Manager) Empty() bool { return len(m.cfgs) == 0 }

// Names lists configured servers.
func (m *Manager) Names() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.cfgs))
	for k := range m.cfgs {
		out = append(out, k)
	}
	return out
}

// connFor returns a live connection, starting one if needed and recycling
// idle ones (SPEC §15 lazy lifecycle).
func (m *Manager) connFor(ctx context.Context, name string) (*conn, error) {
	m.mu.Lock()
	cfg, ok := m.cfgs[name]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("mcp: no server %q (configured: %v)", name, m.Names())
	}
	c := m.conns[name]
	m.mu.Unlock()
	if c != nil {
		c.mu.Lock()
		if time.Since(c.lastUse) < m.idle {
			c.lastUse = time.Now()
			c.mu.Unlock()
			return c, nil
		}
		c.mu.Unlock()
		m.close(name)
	}

	var t transport
	switch cfg.Transport {
	case "http":
		if cfg.URL == "" {
			return nil, fmt.Errorf("mcp: %s: http transport needs url", name)
		}
		t = newHTTPTransport(cfg)
	default: // stdio — NOTE: the exec context must NOT be connFor's
		// request ctx (CommandContext would kill the server the moment
		// the starting call returns); CloseAll reaps it.
		var err error
		t, err = newStdio(context.Background(), cfg)
		if err != nil {
			return nil, err
		}
	}
	cl := &client{t: t}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := cl.handshake(cctx); err != nil {
		_ = t.close()
		return nil, fmt.Errorf("mcp: %s handshake: %w", name, err)
	}
	lctx, lcancel := context.WithTimeout(ctx, 30*time.Second)
	defer lcancel()
	tools, err := cl.listTools(lctx)
	if err != nil {
		_ = t.close()
		return nil, fmt.Errorf("mcp: %s tools/list: %w", name, err)
	}
	nc := &conn{c: cl, tools: tools, lastUse: time.Now()}
	m.mu.Lock()
	m.conns[name] = nc
	m.mu.Unlock()
	return nc, nil
}

func (m *Manager) close(name string) {
	m.mu.Lock()
	c := m.conns[name]
	delete(m.conns, name)
	m.mu.Unlock()
	if c != nil {
		_ = c.c.t.close()
	}
}

// Info starts servers as needed and reports names only (SPEC §15).
func (m *Manager) Info(ctx context.Context) []ServerInfo {
	out := []ServerInfo{}
	for _, name := range m.Names() {
		si := ServerInfo{Name: name, Tools: []string{}}
		c, err := m.connFor(ctx, name)
		if err != nil {
			si.Error = err.Error()
		} else {
			si.Online = true
			for _, t := range c.tools {
				si.Tools = append(si.Tools, t.Name)
			}
		}
		out = append(out, si)
	}
	return out
}

// ToolSchemas returns full definitions for one server (get_mcp_tools_info).
func (m *Manager) ToolSchemas(ctx context.Context, name string) ([]ToolDef, error) {
	c, err := m.connFor(ctx, name)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastUse = time.Now()
	return c.tools, nil
}

// ReadOnly reports the readOnlyHint annotation for gating (SPEC §15).
func (m *Manager) ReadOnly(ctx context.Context, name, tool string) bool {
	tools, err := m.ToolSchemas(ctx, name)
	if err != nil {
		return false
	}
	for _, t := range tools {
		if t.Name == tool {
			return t.Annotations.ReadOnlyHint != nil && *t.Annotations.ReadOnlyHint
		}
	}
	return false
}

// Call executes one tool (call_mcp_tool).
func (m *Manager) Call(ctx context.Context, name, tool string, args json.RawMessage) (CallResult, error) {
	c, err := m.connFor(ctx, name)
	if err != nil {
		return CallResult{}, err
	}
	c.mu.Lock()
	c.lastUse = time.Now()
	c.mu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return c.c.callTool(cctx, tool, args)
}

// CloseAll tears every connection down (shutdown).
func (m *Manager) CloseAll() {
	m.mu.Lock()
	names := make([]string, 0, len(m.conns))
	for k := range m.conns {
		names = append(names, k)
	}
	m.mu.Unlock()
	for _, n := range names {
		m.close(n)
	}
}
