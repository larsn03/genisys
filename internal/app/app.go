// Package app is the wiring layer shared by every front-end (CLI REPL,
// `genisys serve`, tests): it opens the state db, resolves projects and
// sessions, and assembles agent.Deps (store, transcript, grants, shadow
// checkpoints, writer, permission broker, tool registry).
package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/larsn03/genisys/internal/agent"
	"github.com/larsn03/genisys/internal/agent/events"
	"github.com/larsn03/genisys/internal/apiconfig"
	"github.com/larsn03/genisys/internal/mcp"
	"github.com/larsn03/genisys/internal/model"
	"github.com/larsn03/genisys/internal/skills"
	"github.com/larsn03/genisys/internal/store"
	"github.com/larsn03/genisys/internal/tools"
	exectool "github.com/larsn03/genisys/internal/tools/exec"
	"github.com/larsn03/genisys/internal/tools/fileio"
	"github.com/larsn03/genisys/internal/tools/find"
	"github.com/larsn03/genisys/internal/tools/mcptools"
	"github.com/larsn03/genisys/internal/tools/mutate"
	"github.com/larsn03/genisys/internal/tools/perms"
	py "github.com/larsn03/genisys/internal/tools/py"
	"github.com/larsn03/genisys/internal/tools/search"
	"github.com/larsn03/genisys/internal/tools/sysinfo"
	"github.com/larsn03/genisys/internal/tools/ui"
	"github.com/larsn03/genisys/internal/tools/web"
	"github.com/larsn03/genisys/internal/websearch"
	"github.com/larsn03/genisys/internal/workspace"
)

// App owns the process-level singletons.
type App struct {
	Cfg     *apiconfig.Config
	CfgPath string // config.yaml location (genisys_tool config_*)
	DB      *store.DB
	Root    string // ~/.genisys
	MCP     *mcp.Manager
}

// Open loads config, opens+migrates the db and runs the SPEC §23 boot
// sweep (orphan sessions → error, pending permissions → denied).
// Missing config is tolerated (db-only modes: revert, serve --db).
func Open(cfgPath, dbPath string) (*App, error) {
	cfg, err := apiconfig.LoadApp(cfgPath)
	if errors.Is(err, os.ErrNotExist) {
		cfg = (&apiconfig.Config{}).WithDefaults()
	} else if err != nil {
		return nil, err
	}
	if dbPath == "" {
		dbPath = filepath.Join(apiconfig.ExpandPath("~"), ".genisys", "genisys.db")
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.MarkOrphansInterrupted(); err != nil {
		db.Close()
		return nil, err
	}
	if err := db.DenyAllPending(); err != nil {
		db.Close()
		return nil, err
	}
	mgrCfgs := []mcp.ServerConfig{}
	for _, srv := range cfg.MCPServers {
		if !srv.Enabled {
			continue
		}
		mgrCfgs = append(mgrCfgs, mcp.ServerConfig{
			Name: srv.Name, Command: srv.Command, Args: srv.Args, Env: srv.Env,
			URL: srv.URL, Transport: srv.Transport,
		})
	}
	return &App{Cfg: cfg, CfgPath: cfgPath, DB: db, Root: filepath.Dir(dbPath), MCP: mcp.NewManager(mgrCfgs)}, nil
}

// Close releases the db handle and any live MCP connections.
func (a *App) Close() error {
	if a.MCP != nil {
		a.MCP.CloseAll()
	}
	return a.DB.Close()
}

// ProjectFor returns (creating on demand) the project bound to a folder.
func (a *App) ProjectFor(cwd string) (*store.Project, error) {
	clean := filepath.Clean(cwd)
	if p, err := a.DB.GetProjectByWorkspacePath(clean); err == nil {
		return p, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	p := &store.Project{Name: filepath.Base(clean), Kind: "code", WorkspacePath: clean, AllowCoding: true}
	if err := a.DB.CreateProject(p); err != nil {
		return nil, err
	}
	return p, nil
}

// ModelClient builds the endpoint client + tools mode for a model ref.
func (a *App) ModelClient(ref string) (*model.Client, string, error) {
	ep, prof, ok := a.Cfg.ModelRef(ref)
	if !ok {
		return nil, "", fmt.Errorf("model %q not found in config (run `genisys config show`)", ref)
	}
	key := os.Getenv(ep.APIKeyEnv)
	if key == "" {
		key = ep.APIKey
	}
	client, err := model.New(ep.BaseURL, key, model.WithQuirks(prof.Quirks...))
	if err != nil {
		return nil, "", err
	}
	mode := prof.Supports.Tools
	if mode == "" {
		mode = "native"
	}
	return client, mode, nil
}

// ContextWindow returns the configured context window for a model ref
// (0 = unknown; disables threshold compaction, SPEC §24).
func (a *App) ContextWindow(ref string) int {
	if _, prof, ok := a.Cfg.ModelRef(ref); ok {
		return prof.ContextWindow
	}
	return 0
}

// Registry assembles the tool surface. WithUI adds the panel/delivery
// tools (server mode); web_search registers only when enabled+configured
// (SPEC §9.5).
func (a *App) Registry(withUI bool) *tools.Registry {
	r := tools.NewRegistry()
	r.Add(fileio.ReadLines{})
	r.Add(fileio.ReadChars{})
	r.Add(fileio.ListDir{})
	r.Add(search.SearchLine{})
	r.Add(search.FindFiles{})
	r.Add(exectool.Shell{})
	r.Add(mutate.EditFile{})
	r.Add(mutate.ReplaceFile{})
	r.Add(mutate.Delete{})
	r.Add(mutate.Move{})
	r.Add(mutate.Copy{})
	r.Add(mutate.CreateFolder{})
	r.Add(perms.ReadOnlyAccess())
	r.Add(perms.ReadWriteAccess())
	r.Add(perms.ScratchpadFolder{})
	r.Add(py.Tool{})
	r.Add(py.LoadAttachment{})
	r.Add(find.Find{})
	if a.MCP != nil && !a.MCP.Empty() {
		for _, t := range mcptools.Tools() {
			r.Add(t)
		}
	}
	r.Add(sysinfo.Tool{}) // SPEC §9.8 introspection
	if withUI {
		for _, t := range ui.Tools() {
			r.Add(t)
		}
	}
	if c := a.Websearch(); c != nil {
		r.Add(web.Search{C: c})
		r.Add(web.Extract{})
	}
	return r
}

// subagentModel resolves agent.subagent_model (SPEC §12): null = same
// model as the session; otherwise a full "endpoint/model" ref.
func subagentModel(a *App, sessionRef string) string {
	if a.Cfg.Agent.SubagentModel == nil {
		return ""
	}
	m := *a.Cfg.Agent.SubagentModel
	if m == "" || m == sessionRef {
		return ""
	}
	return m
}

// SkillUserRoot is where user skills and built-ins live (SPEC §14).
func (a *App) SkillUserRoot() string { return filepath.Join(a.Root, "skills") }

// Skills materializes built-ins and discovers all enabled roots
// (SPEC §14): builtin → project → user → imported (first name wins).
func (a *App) Skills(proj *store.Project) []skills.Skill {
	builtinRoot := filepath.Join(a.SkillUserRoot(), "_builtin")
	skills.MaterializeBuiltin(builtinRoot)
	var roots []string
	if proj != nil {
		roots = append(roots, filepath.Join(proj.WorkspacePath, "skills"))
	}
	roots = append(roots, a.SkillUserRoot())
	for _, r := range a.Cfg.Skills.ImportFromOtherApps {
		roots = append(roots, apiconfig.ExpandPath(r))
	}
	out := skills.Discover(append([]string{builtinRoot}, roots...)...)
	for i := range out {
		out[i].Builtin = out[i].Root == builtinRoot
	}
	return out
}

// Websearch builds the configured provider client or nil (SPEC §9.5/§16).
// provider "none" or a missing token/base_url disables the web tools
// entirely — the toggle removes them from the schema.
func (a *App) Websearch() *websearch.Client {
	if !a.Cfg.Web.SearchEnabled {
		return nil
	}
	var cfg websearch.Config
	switch a.Cfg.Web.Provider {
	case "tavily":
		cfg = websearch.Config{Provider: "tavily", BaseURL: a.Cfg.Web.Tavily.BaseURL,
			APIKeyEnv: a.Cfg.Web.Tavily.APIKeyEnv, TimeoutMs: 10000}
	case "brave":
		cfg = websearch.Config{Provider: "brave", BaseURL: a.Cfg.Web.Brave.BaseURL,
			APIKeyEnv: a.Cfg.Web.Brave.APIKeyEnv, TimeoutMs: 10000}
	case "none":
		return nil
	default: // searxng
		if a.Cfg.Web.Searxng.BaseURL == "" {
			return nil
		}
		cfg = websearch.Config{
			Provider: "searxng", BaseURL: a.Cfg.Web.Searxng.BaseURL,
			Language: a.Cfg.Web.Searxng.Language, Categories: a.Cfg.Web.Searxng.Categories,
			TimeoutMs: a.Cfg.Web.Searxng.TimeoutMs, APIKeyEnv: a.Cfg.Web.Searxng.APIKeyEnv,
			AllowInsecureTLS: a.Cfg.Web.Searxng.AllowInsecureTLS,
		}
	}
	c, err := websearch.New(cfg)
	if err != nil {
		return nil
	}
	return c
}

// SessionOpts parameterizes OpenSession.
type SessionOpts struct {
	Emit      events.Sink
	Ask       workspace.Asker    // nil = headless (permission requests deny)
	Registry  *tools.Registry    // nil = a.Registry(false)
	ModelRef  string             // "" = the session row's model_ref
	ToolsMode string             // "" = the profile's mode (native|xml|none)
	Cwd       string             // "" = the project's workspace path
	Interj    *agent.Interjector // nil = a fresh queue (CLI; the supervisor injects its own)
}

// Handle is a live session with all its collaborators.
type Handle struct {
	Sess       *agent.Session
	Row        *store.Session
	Project    *store.Project
	Writer     *workspace.Writer
	Broker     *workspace.Broker
	Shadow     *workspace.ShadowRepo
	Transcript *store.Transcript
	Scratch    string
	Cwd        string
}

// OpenSession wires store + transcript + grants + shadow + writer + broker
// for a session row (resume-aware: replays the transcript).
func (a *App) OpenSession(row *store.Session, proj *store.Project, o SessionOpts) (*Handle, error) {
	if o.Emit == nil {
		o.Emit = events.Discard
	}
	if o.Registry == nil {
		o.Registry = a.Registry(false)
	}
	ref := o.ModelRef
	if ref == "" {
		ref = row.ModelRef
	}
	client, mode, err := a.ModelClient(ref)
	if err != nil {
		return nil, err
	}
	if o.ToolsMode != "" {
		mode = o.ToolsMode
	}
	cwd := o.Cwd
	if cwd == "" {
		if proj == nil {
			return nil, errors.New("app: free-chat sessions need SessionOpts.Cwd")
		}
		cwd = proj.WorkspacePath
	}
	scratch := filepath.Join(a.Root, "scratchpad", row.ID)
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return nil, err
	}

	grants, err := workspace.NewGrants(cwd, scratch, true)
	if err != nil {
		return nil, err
	}
	if granted, err := a.DB.GrantedPaths(row.ID); err == nil {
		grants.LoadGrants(granted) // session-scoped grants survive resume
	}
	var cp *workspace.ShadowRepo
	if proj != nil {
		cp, err = workspace.OpenShadow(filepath.Join(a.Root, "projects", proj.ID, "checkpoints"), cwd)
		if err != nil {
			return nil, err
		}
	}
	writer := workspace.NewWriter(workspace.WriterOpts{
		Grants: grants, Cp: cp, DB: a.DB,
		ProjectID: projectID(proj), SessionID: row.ID, Emit: o.Emit,
	})
	broker := workspace.NewBroker(grants, a.DB, row.ID, 0, o.Ask, o.Emit)

	var tr *store.Transcript
	tpath := agent.TranscriptPath(filepath.Join(a.Root, "projects", projectID(proj)), "", row.ID)
	if proj == nil {
		tpath = agent.TranscriptPath(a.Root, "", row.ID)
	}
	tr, err = store.OpenTranscript(tpath)
	if err != nil {
		return nil, err
	}

	gen := a.genisysFacade(row, proj, ref, cwd, scratch, cp, writer)

	deps := agent.Deps{
		Gen:    gen,
		Client: client, Model: ref, Tools: o.Registry, ToolsMode: mode,
		MaxSteps: a.Cfg.Agent.MaxSteps, ShellTimeout: a.Cfg.Agent.ShellTimeoutMs,
		ContextWindow:  a.ContextWindow(ref),
		RuntimeDir:     filepath.Join(a.Root, "runtime"),
		Persona:        row.Persona,
		Skills:         a.Skills(proj),
		SubagentModel:  subagentModel(a, ref),
		MCP:            a.MCP,
		BrowserOrigins: a.Cfg.Browser.AllowedOrigins,
		Cwd:            cwd, Scratchpad: scratch, Events: o.Emit,
		SessionID: row.ID, Transcript: tr, DB: a.DB, ProjectID: projectID(proj),
		Writer: writer, Perms: broker, Interj: o.Interj,
	}
	var sess *agent.Session
	if tr != nil {
		if fi, serr := os.Stat(tpath); serr == nil && fi.Size() > 0 {
			sess, err = agent.Resume(deps)
			if err != nil {
				tr.Close()
				return nil, err
			}
		} else {
			sess = agent.NewSession(deps)
		}
	} else {
		sess = agent.NewSession(deps)
	}
	return &Handle{
		Sess: sess, Row: row, Project: proj, Writer: writer, Broker: broker,
		Shadow: cp, Transcript: tr, Scratch: scratch, Cwd: cwd,
	}, nil
}

// CloseSession releases the transcript handle.
func (h *Handle) Close() {
	if h.Transcript != nil {
		h.Transcript.Close()
	}
}

// genisysFacade wires the SPEC §9.8 introspection surface for one
// session. Everything is closure-bound so the tool package stays dumb.
func (a *App) genisysFacade(row *store.Session, proj *store.Project, ref, cwd, scratch string,
	cp *workspace.ShadowRepo, writer *workspace.Writer) *tools.Genisys {
	ctxbg := context.Background()
	g := &tools.Genisys{}

	g.ConfigYAML = func() (string, error) {
		b, err := os.ReadFile(a.CfgPath)
		return string(b), err
	}
	g.ConfigSet = func(path, value string) error {
		if err := apiconfig.Update(a.CfgPath, func(doc *yaml.Node) error {
			return apiconfig.Set(doc, strings.Split(path, "."), value)
		}); err != nil {
			return err
		}
		return a.ReloadeCfg()
	}
	g.Reload = a.ReloadeCfg

	g.SessionInfo = func() (map[string]any, error) {
		m := map[string]any{
			"session_id": row.ID, "title": row.Title, "model_ref": ref,
			"status": row.Status, "cwd": cwd, "scratchpad": scratch,
			"persona": row.Persona, "forked_from": row.ForkedFrom,
		}
		if proj != nil {
			m["project"] = map[string]any{"id": proj.ID, "name": proj.Name,
				"kind": proj.Kind, "workspace": proj.WorkspacePath}
		}
		return m, nil
	}

	g.SessionStats = func() (map[string]any, error) {
		evs, err := a.DB.ListEvents(row.ID)
		if err != nil {
			return nil, err
		}
		var prompt, completion, steps, toolCalls int64
		for _, ev := range evs {
			var p map[string]any
			_ = json.Unmarshal([]byte(ev.PayloadJSON), &p)
			switch ev.Type {
			case "usage":
				prompt += int64(num(p["prompt_tokens"]))
				completion += int64(num(p["completion_tokens"]))
			case "step_finished":
				steps++
			case "tool_started":
				toolCalls++
			}
		}
		return map[string]any{
			"prompt_tokens": prompt, "completion_tokens": completion,
			"steps": steps, "tool_calls": toolCalls, "events": len(evs),
		}, nil
	}

	g.SessionList = func() ([]map[string]any, error) {
		rows, err := a.DB.ListSessions()
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for i, sr := range rows {
			if i >= 25 {
				break
			}
			out = append(out, map[string]any{"id": sr.ID, "title": sr.Title,
				"model_ref": sr.ModelRef, "status": sr.Status, "updated": sr.UpdatedAt})
		}
		return out, nil
	}

	g.ModelSet = func(newRef string) error {
		if _, _, err := a.ModelClient(newRef); err != nil {
			return err
		}
		return a.DB.SetSessionModel(row.ID, newRef)
	}

	g.Compact = func() (map[string]any, error) {
		sess, err := a.openSessFor(row, proj, ref)
		if err != nil {
			return nil, err
		}
		if err := sess.CompactNow(ctxbg); err != nil {
			return nil, err
		}
		return map[string]any{"compacted": true}, nil
	}

	g.Checkpoints = func() ([]map[string]any, error) {
		if proj == nil {
			return []map[string]any{}, nil
		}
		cps, err := a.DB.ListCheckpoints(proj.ID)
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for i, c := range cps {
			if i >= 30 {
				break
			}
			out = append(out, map[string]any{"sha": c.SHA, "step": c.StepID,
				"label": c.Label, "when": c.CreatedAt})
		}
		return out, nil
	}

	g.FileHistory = func(path string) ([]map[string]any, error) {
		if cp == nil {
			return nil, fmt.Errorf("no checkpoints for this session")
		}
		hist, err := cp.FileHistory(path, 50)
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for _, c := range hist {
			out = append(out, map[string]any{"sha": c.SHA, "label": c.Label,
				"when": c.When.UTC().Format(time.RFC3339)})
		}
		return out, nil
	}

	g.FileRestore = func(path, sha string, force bool) error {
		if cp == nil {
			return fmt.Errorf("no checkpoints for this session")
		}
		abs := filepath.Join(cwd, filepath.FromSlash(path))
		if !force {
			if cur, err := os.ReadFile(abs); err == nil {
				if last, known := writer.LastWritten(abs); known && last != fmt.Sprintf("%x", sha256.Sum256(cur)) {
					return fmt.Errorf("user edits since the last checkpoint would be overwritten — pass force=true")
				}
			}
		}
		return writer.Mutate(ctxbg, "restore", "restore "+path, []string{abs},
			func(*workspace.Txn) error { return cp.Restore(sha, []string{path}) })
	}

	g.SkillsList = func() ([]map[string]any, error) {
		out := []map[string]any{}
		for _, sk := range a.Skills(proj) {
			out = append(out, map[string]any{"name": sk.Name, "description": sk.Description,
				"builtin": sk.Builtin, "path": sk.Path})
		}
		return out, nil
	}
	g.SkillInstall = func(url, content string) (string, error) {
		if content == "" && url != "" {
			req, err := http.NewRequestWithContext(ctxbg, "GET", url, nil)
			if err != nil {
				return "", err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return "", err
			}
			defer resp.Body.Close()
			b, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
			if err != nil {
				return "", err
			}
			content = string(b)
		}
		if content == "" {
			return "", fmt.Errorf("provide url or content")
		}
		sk, err := skills.Install(a.SkillUserRoot(), content)
		return sk.Name, err
	}
	g.SkillRemove = func(name string) error {
		for _, sk := range a.Skills(proj) {
			if sk.Name == name {
				return skills.Remove(sk)
			}
		}
		return fmt.Errorf("skill %q not found", name)
	}

	g.MCPStatus = func() []map[string]any {
		out := []map[string]any{}
		for _, si := range a.MCP.Info(ctxbg) {
			out = append(out, map[string]any{"name": si.Name, "online": si.Online,
				"error": si.Error, "tools": si.Tools})
		}
		return out
	}

	g.WebProbe = func() (map[string]any, error) {
		c := a.Websearch()
		if c == nil {
			return map[string]any{"enabled": false,
				"hint": "set web.provider + web.searxng.base_url in config.yaml"}, nil
		}
		return map[string]any{"enabled": true, "probe": c.Probe(ctxbg)}, nil
	}

	g.Models = func() []map[string]any {
		out := []map[string]any{}
		for _, ep := range a.Cfg.Endpoints {
			for _, m := range ep.Models {
				out = append(out, map[string]any{
					"ref": ep.ID + "/" + m.ID, "context_window": m.ContextWindow})
			}
		}
		return out
	}
	return g
}

// ReloadeCfg re-parses + validates config.yaml and swaps it in (hot
// reload; genisys_tool config_set / config_validate).
func (a *App) ReloadeCfg() error {
	cfg, err := apiconfig.LoadApp(a.CfgPath)
	if err != nil {
		return err
	}
	if issues := apiconfig.ValidateApp(cfg, a.CfgPath); len(issues) > 0 {
		return apiconfig.Issues(issues)
	}
	a.Cfg = cfg
	return nil
}

// openSessFor builds a throwaway session handle for out-of-band actions
// (genisys_tool compact) when the caller has no live Loop at hand.
func (a *App) openSessFor(row *store.Session, proj *store.Project, ref string) (*agent.Session, error) {
	h, err := a.OpenSession(row, proj, SessionOpts{ModelRef: ref})
	if err != nil {
		return nil, err
	}
	return h.Sess, nil
}

func num(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

func projectID(p *store.Project) string {
	if p == nil {
		return ""
	}
	return p.ID
}
