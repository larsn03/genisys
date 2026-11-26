// rest.go — CRUD + control endpoints (SPEC §19).

package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/go-git/go-git/v5"

	"github.com/larsn03/genisys/internal/agent"
	"github.com/larsn03/genisys/internal/apiconfig"
	"github.com/larsn03/genisys/internal/app"
	"github.com/larsn03/genisys/internal/model"
	"github.com/larsn03/genisys/internal/skills"
	"github.com/larsn03/genisys/internal/store"
	"github.com/larsn03/genisys/internal/supervisor"
	"github.com/larsn03/genisys/internal/workspace"
)

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"name": "genisys", "version": Version})
	})
	m.HandleFunc("GET /api/models", s.listModels)

	// native window chrome (frameless desktop build only)
	notShell := func(w http.ResponseWriter) { http.Error(w, "no native window", 404) }
	m.HandleFunc("GET /api/window/state", func(w http.ResponseWriter, _ *http.Request) {
		if s.WinCtl == nil {
			notShell(w)
			return
		}
		writeJSON(w, 200, map[string]bool{"maximized": s.WinCtl.IsMaximized()})
	})
	m.HandleFunc("POST /api/window/minimize", func(w http.ResponseWriter, _ *http.Request) {
		if s.WinCtl == nil {
			notShell(w)
			return
		}
		s.WinCtl.Minimize()
		w.WriteHeader(204)
	})
	m.HandleFunc("POST /api/window/toggle-maximize", func(w http.ResponseWriter, _ *http.Request) {
		if s.WinCtl == nil {
			notShell(w)
			return
		}
		s.WinCtl.ToggleMaximize()
		w.WriteHeader(204)
	})
	m.HandleFunc("POST /api/window/close", func(w http.ResponseWriter, _ *http.Request) {
		if s.WinCtl == nil {
			notShell(w)
			return
		}
		s.WinCtl.Close()
		w.WriteHeader(204)
	})
	m.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"running": s.Sup.RunningSessions(), "queued": s.Sup.Queued(), "cap": s.Sup.Cap,
		})
	})

	// config (settings UI): GET raw YAML; PATCH dotted keys comment-preservingly
	m.HandleFunc("GET /api/config", s.getConfig)
	m.HandleFunc("PATCH /api/config", s.patchConfig)
	m.HandleFunc("PUT /api/config", s.putConfig)
	m.HandleFunc("GET /api/settings", s.getSettings)

	m.HandleFunc("GET /api/projects", s.listProjects)
	m.HandleFunc("POST /api/projects", s.createProject)
	m.HandleFunc("GET /api/projects/{id}", s.getProject)
	m.HandleFunc("DELETE /api/projects/{id}", s.deleteProject)

	m.HandleFunc("GET /api/sessions", s.listSessions)
	m.HandleFunc("POST /api/sessions", s.createSession)
	m.HandleFunc("GET /api/sessions/{id}", s.getSession)
	m.HandleFunc("PATCH /api/sessions/{id}", s.patchSession)
	m.HandleFunc("DELETE /api/sessions/{id}", s.archiveSession)
	m.HandleFunc("POST /api/sessions/{id}/fork", s.forkSession)

	m.HandleFunc("GET /api/sessions/{id}/messages", s.sessionMessages)
	m.HandleFunc("GET /api/sessions/{id}/events", s.sessionEvents)
	m.HandleFunc("POST /api/sessions/{id}/prompt", s.prompt)
	m.HandleFunc("POST /api/sessions/{id}/stop", s.stop)
	m.HandleFunc("POST /api/sessions/{id}/compact", s.compact)
	m.HandleFunc("GET /api/sessions/{id}/interjections", s.listInterjections)
	m.HandleFunc("POST /api/sessions/{id}/interjections/{ij}/{op}", s.interjectionOp)
	m.HandleFunc("POST /api/sessions/{id}/permissions/{pid}", s.answerPermission)
	m.HandleFunc("GET /api/sessions/{id}/checkpoints", s.listCheckpoints)
	m.HandleFunc("GET /api/sessions/{id}/git-status", s.gitStatus)
	m.HandleFunc("GET /api/sessions/{id}/file-history", s.fileHistory)
	m.HandleFunc("GET /api/sessions/{id}/file-at", s.fileAt)
	m.HandleFunc("POST /api/sessions/{id}/restore-file", s.restoreFile)
	m.HandleFunc("POST /api/sessions/{id}/revert", s.revert)
	m.HandleFunc("GET /api/sessions/{id}/files", s.listFiles)
	m.HandleFunc("GET /api/sessions/{id}/file", s.getFile)
	m.HandleFunc("PUT /api/sessions/{id}/file", s.putFile)

	m.HandleFunc("POST /api/sessions/{id}/upload", s.upload)
	m.HandleFunc("GET /api/sessions/{id}/preview", s.preview)

	m.HandleFunc("POST /api/web/probe", s.webProbe)
	m.HandleFunc("POST /api/endpoints/{id}/test", s.testEndpoint)
	m.HandleFunc("POST /api/endpoints/test-url", s.testEndpointURL)
	m.HandleFunc("GET /api/browser/origins", func(w http.ResponseWriter, r *http.Request) {
		origins := s.App.Cfg.Browser.AllowedOrigins
		if origins == nil {
			origins = []string{}
		}
		writeJSON(w, 200, map[string]any{"origins": origins})
	})
	m.HandleFunc("GET /api/skills", s.listSkills)
	m.HandleFunc("POST /api/skills/install", s.installSkill)
	m.HandleFunc("DELETE /api/skills/{name}", s.deleteSkill)
	m.HandleFunc("GET /ws", s.ws)
	m.HandleFunc("GET /", s.serveUI)
}

// Version is stamped by cmd at startup.
var Version = "dev"

// --- config -------------------------------------------------------------------

// testEndpointURL validates a base URL typed into onboarding before it
// is ever written to config.yaml.
func (s *Server) testEndpointURL(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BaseURL string `json:"base_url"`
	}
	if err := readJSON(r, &in); err != nil || in.BaseURL == "" {
		writeErr(w, 400, "base_url is required")
		return
	}
	client, err := model.New(in.BaseURL, "")
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	models, err := client.ListModels(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "models": models})
}

// testEndpoint: settings "Test connection" (SPEC §5.5). Cheap by default
// (GET /models); ?probe=<model_id> additionally runs the capability probe.
func (s *Server) testEndpoint(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var ep *apiconfig.Endpoint
	for i := range s.App.Cfg.Endpoints {
		if s.App.Cfg.Endpoints[i].ID == id {
			ep = &s.App.Cfg.Endpoints[i]
			break
		}
	}
	if ep == nil {
		writeErr(w, 404, "endpoint "+id+" not in config")
		return
	}
	key := os.Getenv(ep.APIKeyEnv)
	if key == "" {
		key = ep.APIKey
	}
	client, err := model.New(ep.BaseURL, key)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out := map[string]any{"endpoint": id, "base_url": ep.BaseURL}
	models, err := client.ListModels(ctx)
	if err != nil {
		out["ok"] = false
		out["error"] = err.Error()
		writeJSON(w, 200, out)
		return
	}
	out["ok"] = true
	out["models"] = models
	if probeID := r.URL.Query().Get("probe"); probeID != "" {
		res, err := client.Probe(ctx, probeID)
		if err != nil {
			out["probe_error"] = err.Error()
		} else {
			out["probe"] = res
		}
	}
	writeJSON(w, 200, out)
}

// webProbe powers the settings screen "Test" button (SPEC §5.5 search
// tab; same classification as `genisys search test`).
func (s *Server) webProbe(w http.ResponseWriter, r *http.Request) {
	c := s.App.Websearch()
	if c == nil {
		writeJSON(w, 200, map[string]any{"enabled": false,
			"message": "web search disabled — set web.provider and web.searxng.base_url"})
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": true, "probe": c.Probe(r.Context())})
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(s.ConfigPath)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/yaml")
	w.WriteHeader(200)
	w.Write(data)
}

// getSettings returns the structured view of config.yaml the settings
// screens render (SPEC §5.5). Editing still goes through PATCH (dotted
// keys) or PUT (raw YAML) so comments in the file survive.
func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	c := s.App.Cfg
	eps := []map[string]any{}
	for _, ep := range c.Endpoints {
		mods := []map[string]any{}
		for _, m := range ep.Models {
			mods = append(mods, map[string]any{
				"id": m.ID, "context_window": m.ContextWindow,
				"tools": m.Supports.Tools, "vision": m.Supports.Vision,
			})
		}
		eps = append(eps, map[string]any{
			"id": ep.ID, "name": ep.Name, "base_url": ep.BaseURL,
			"api_key_env": ep.APIKeyEnv, "models": mods,
		})
	}
	mcps := []map[string]any{}
	for _, m := range c.MCPServers {
		mcps = append(mcps, map[string]any{
			"name": m.Name, "command": m.Command, "args": m.Args,
			"url": m.URL, "transport": m.Transport, "enabled": m.Enabled,
		})
	}
	origins := c.Browser.AllowedOrigins
	if origins == nil {
		origins = []string{}
	}
	imports := c.Skills.ImportFromOtherApps
	if imports == nil {
		imports = []string{}
	}
	writeJSON(w, 200, map[string]any{
		"default_model": c.DefaultModel,
		"agent": map[string]any{
			"max_steps": c.Agent.MaxSteps, "shell_timeout_ms": c.Agent.ShellTimeoutMs,
			"subagent_model": deref(c.Agent.SubagentModel),
			"interjection": map[string]any{
				"default_mode": c.Agent.Interjection.DefaultMode,
				"boundary":     c.Agent.Interjection.Boundary,
			},
		},
		"web": map[string]any{
			"search_enabled": c.Web.SearchEnabled, "provider": c.Web.Provider,
			"searxng": map[string]any{
				"base_url": c.Web.Searxng.BaseURL, "language": c.Web.Searxng.Language,
				"categories": c.Web.Searxng.Categories, "timeout_ms": c.Web.Searxng.TimeoutMs,
			},
			"tavily": map[string]any{"api_key_env": c.Web.Tavily.APIKeyEnv},
			"brave":  map[string]any{"api_key_env": c.Web.Brave.APIKeyEnv},
		},
		"browser":     map[string]any{"allowed_origins": origins},
		"skills":      map[string]any{"import_from_other_apps": imports},
		"mcp_servers": mcps,
		"endpoints":   eps,
		"ui":          map[string]any{"theme": c.UI.Theme},
	})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// putConfig replaces config.yaml wholesale (settings "Advanced" tab).
// Parse+validate BEFORE writing: a broken save can never brick boot.
func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		YAML string `json:"yaml"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg, err := apiconfig.ParseApp(s.ConfigPath, []byte(in.YAML))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	tmp := s.ConfigPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(in.YAML), 0o644); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if err := os.Rename(tmp, s.ConfigPath); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.App.Cfg = cfg
	s.getConfig(w, r)
}

// (patchConfig below uses apiconfig.GetNode to keep sequences intact)

func (s *Server) patchConfig(w http.ResponseWriter, r *http.Request) {
	var kv map[string]string
	if err := readJSON(r, &kv); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	path := s.ConfigPath
	var newCfg *apiconfig.Config
	err := apiconfig.Update(path, func(doc *yaml.Node) error {
		for k, v := range kv {
			keys := strings.Split(k, ".")
			// list-valued keys (browser.allowed_origins…) take a
			// comma-separated value and stay sequences
			if cur, ok := apiconfig.GetNode(doc, keys); ok && cur.Kind == yaml.SequenceNode {
				vals := []string{}
				for _, part := range strings.Split(v, ",") {
					if t := strings.TrimSpace(part); t != "" {
						vals = append(vals, t)
					}
				}
				if err := apiconfig.SetList(doc, keys, vals); err != nil {
					return err
				}
				continue
			}
			if err := apiconfig.Set(doc, keys, v); err != nil {
				return err
			}
		}
		// validate before write: an invalid patch must not touch the
		// file nor the running config (previously the file was written
		// first, so e.g. provider=searxng without base_url half-applied
		// and the UI silently reverted while the file had changed)
		cfg, err := apiconfig.ParseAppNode(path, doc)
		if err != nil {
			return err
		}
		newCfg = cfg
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if newCfg != nil {
		s.App.Cfg = newCfg // running server picks up web/agent changes
	}
	s.getConfig(w, r)
}

// --- projects -------------------------------------------------------------------

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	// store has no list-projects yet; derive from sessions + all rows
	rows, err := s.App.DB.ListProjects()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if rows == nil {
		rows = []*store.Project{} // JSON [] not null
	}
	writeJSON(w, 200, rows)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var in store.Project
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if in.WorkspacePath == "" || in.Kind == "" {
		writeErr(w, 400, "workspace_path and kind are required")
		return
	}
	if err := s.App.DB.CreateProject(&in); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, in)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.App.DB.GetProject(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	if err := s.App.DB.DeleteProject(r.PathValue("id")); err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// --- sessions -------------------------------------------------------------------

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.App.DB.ListSessions()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	type view struct {
		*store.Session
		Live bool `json:"live"`
	}
	out := make([]view, 0, len(rows))
	for _, row := range rows {
		wk, err := s.Sup.Worker(row.ID)
		live := err == nil && wk.Running()
		out = append(out, view{row, live})
	}
	writeJSON(w, 200, out)
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProjectID string `json:"project_id"`
		ModelRef  string `json:"model_ref"`
		Title     string `json:"title"`
		Persona   string `json:"persona"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ref := in.ModelRef
	if ref == "" {
		ref = s.App.Cfg.DefaultModel
	}
	if ref == "" {
		writeErr(w, 400, "model_ref required (no default_model configured)")
		return
	}
	row := &store.Session{ProjectID: in.ProjectID, ModelRef: ref, Title: in.Title, Persona: in.Persona}
	if err := s.App.DB.CreateSession(row); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, row)
}

func (s *Server) sessionRow(w http.ResponseWriter, r *http.Request) *store.Session {
	row, err := s.App.DB.GetSession(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "session not found")
		return nil
	}
	return row
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	row := s.sessionRow(w, r)
	if row == nil {
		return
	}
	wk, _ := s.Sup.Worker(row.ID)
	writeJSON(w, 200, map[string]any{"session": row, "live": wk != nil && wk.Running()})
}

func (s *Server) patchSession(w http.ResponseWriter, r *http.Request) {
	row := s.sessionRow(w, r)
	if row == nil {
		return
	}
	var in struct {
		Title    *string `json:"title"`
		Pinned   *bool   `json:"pinned"`
		ModelRef *string `json:"model_ref"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if in.ModelRef != nil {
		if err := s.App.DB.SetSessionModel(row.ID, *in.ModelRef); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if in.Title != nil {
		_ = s.App.DB.SetSessionTitle(row.ID, *in.Title)
	}
	if in.Pinned != nil {
		_ = s.App.DB.SetSessionPinned(row.ID, *in.Pinned)
	}
	updated, _ := s.App.DB.GetSession(row.ID)
	writeJSON(w, 200, updated)
}

func (s *Server) archiveSession(w http.ResponseWriter, r *http.Request) {
	row := s.sessionRow(w, r)
	if row == nil {
		return
	}
	if wk, err := s.Sup.Worker(row.ID); err == nil {
		wk.Close()
	}
	// ?hard=1 deletes for real (SPEC §5.2: Archive vs Delete)
	if r.URL.Query().Get("hard") == "1" {
		if err := s.App.DB.DeleteSession(row.ID); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		_ = os.RemoveAll(filepath.Dir(agent.TranscriptPath(s.App.Root, row.ProjectID, row.ID)))
		writeJSON(w, 200, map[string]bool{"ok": true, "hard": true})
		return
	}
	if err := s.App.DB.SetSessionStatus(row.ID, store.StatusArchived); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// forkSession branches a session: fresh row + copied transcript + copied
// message rows (SPEC §5.2). Forking mid-run is safe — the source keeps
// running and appending; the fork branches from committed state.
func (s *Server) forkSession(w http.ResponseWriter, r *http.Request) {
	row := s.sessionRow(w, r)
	if row == nil {
		return
	}
	var in struct {
		Title string `json:"title"`
	}
	_ = readJSON(r, &in)
	title := in.Title
	if title == "" {
		title = row.Title + " (fork)"
	}
	fork := &store.Session{
		ProjectID: row.ProjectID, Title: title, Persona: row.Persona,
		ModelRef: row.ModelRef, ForkedFrom: row.ID,
	}
	if err := s.App.DB.CreateSession(fork); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	src := agent.TranscriptPath(s.App.Root, row.ProjectID, row.ID)
	dst := agent.TranscriptPath(s.App.Root, fork.ProjectID, fork.ID)
	if b, err := os.ReadFile(src); err == nil {
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		_ = os.WriteFile(dst, b, 0o644)
	}
	if err := s.App.DB.CopyMessages(row.ID, fork.ID); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, fork)
}

func (s *Server) sessionMessages(w http.ResponseWriter, r *http.Request) {
	row := s.sessionRow(w, r)
	if row == nil {
		return
	}
	msgs, err := s.App.DB.ListMessages(row.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if msgs == nil {
		msgs = []store.MessageRow{}
	}
	writeJSON(w, 200, msgs)
}

func (s *Server) sessionEvents(w http.ResponseWriter, r *http.Request) {
	row := s.sessionRow(w, r)
	if row == nil {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	evs, err := s.App.DB.ListEventsAfter(row.ID, after)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if evs == nil {
		evs = []store.EventRow{}
	}
	writeJSON(w, 200, evs)
}

// --- control ---------------------------------------------------------------------

// listSkills feeds the @-palette and settings (SPEC §14).
func (s *Server) listSkills(w http.ResponseWriter, r *http.Request) {
	var proj *store.Project
	if pid := r.URL.Query().Get("project"); pid != "" {
		if p, err := s.App.DB.GetProject(pid); err == nil {
			proj = p
		}
	}
	out := s.App.Skills(proj)
	if out == nil {
		out = []skills.Skill{}
	}
	writeJSON(w, 200, map[string]any{"skills": out})
}

// installSkill fetches a raw SKILL.md URL, validates, and places it in
// the user skills root (SPEC §14 skill-management flow, server-side twin).
func (s *Server) installSkill(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL     string `json:"url"`
		Content string `json:"content"`
	}
	if err := readJSON(r, &in); err != nil || (in.URL == "" && in.Content == "") {
		writeErr(w, 400, "url or content is required")
		return
	}
	content := in.Content
	if content == "" {
		if !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
			writeErr(w, 400, "url must be http(s)")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			writeErr(w, 502, "fetch failed: "+err.Error())
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			writeErr(w, 502, fmt.Sprintf("fetch returned HTTP %d", resp.StatusCode))
			return
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			writeErr(w, 502, "fetch truncated: "+err.Error())
			return
		}
		content = string(b)
	}
	sk, err := skills.Install(s.App.SkillUserRoot(), content)
	if err != nil {
		writeErr(w, 422, err.Error())
		return
	}
	writeJSON(w, 201, sk)
}

func (s *Server) deleteSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	for _, sk := range s.App.Skills(nil) {
		if sk.Name == name {
			if err := skills.Remove(sk); err != nil {
				writeErr(w, 422, err.Error())
				return
			}
			writeJSON(w, 200, map[string]string{"removed": name})
			return
		}
	}
	writeErr(w, 404, "skill not found")
}

func (s *Server) prompt(w http.ResponseWriter, r *http.Request) {
	row := s.sessionRow(w, r)
	if row == nil {
		return
	}
	var in struct {
		Text   string   `json:"text"`
		Mode   string   `json:"mode"`   // queue | interrupt (default queue)
		Skills []string `json:"skills"` // explicit skill requests (SPEC §14)
	}
	if err := readJSON(r, &in); err != nil || in.Text == "" {
		writeErr(w, 400, "text is required")
		return
	}
	if names := in.Skills; len(names) > 0 {
		avail := s.App.Skills(nil)
		if p, perr := s.App.DB.GetSession(row.ID); perr == nil {
			if proj, perr2 := s.App.DB.GetProject(p.ProjectID); perr2 == nil {
				avail = s.App.Skills(proj)
			}
		}
		byName := map[string]skills.Skill{}
		for _, sk := range avail {
			byName[sk.Name] = sk
		}
		for _, n := range names {
			if sk, ok := byName[n]; ok {
				in.Text += "\n" + skills.Instruction(sk)
			}
		}
	}
	wk, err := s.Sup.Worker(row.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	res, err := wk.Prompt(context.Background(), in.Text, in.Mode)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	state := "started"
	switch res {
	case supervisor.Interjected:
		state = "interjected"
	case supervisor.Queued:
		state = "queued"
	}
	writeJSON(w, 202, map[string]string{"state": state})
}

func (s *Server) compact(w http.ResponseWriter, r *http.Request) {
	row := s.sessionRow(w, r)
	if row == nil {
		return
	}
	wk, err := s.Sup.Worker(row.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if err := wk.Compact(r.Context()); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"state": "compacted"})
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	row := s.sessionRow(w, r)
	if row == nil {
		return
	}
	wk, err := s.Sup.Worker(row.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"stopped": wk.Stop()})
}

func (s *Server) listInterjections(w http.ResponseWriter, r *http.Request) {
	row := s.sessionRow(w, r)
	if row == nil {
		return
	}
	wk, err := s.Sup.Worker(row.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	pending := wk.Interjections().Pending()
	if pending == nil {
		pending = []agent.Interjection{}
	}
	writeJSON(w, 200, pending)
}

func (s *Server) interjectionOp(w http.ResponseWriter, r *http.Request) {
	row := s.sessionRow(w, r)
	if row == nil {
		return
	}
	wk, err := s.Sup.Worker(row.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	ij, op := r.PathValue("ij"), r.PathValue("op")
	var ok bool
	switch op {
	case "withdraw":
		ok = wk.Interjections().Withdraw(ij)
	case "promote":
		ok = wk.Interjections().Promote(ij)
	default:
		writeErr(w, 400, "op must be withdraw|promote")
		return
	}
	if !ok {
		writeErr(w, 404, "interjection not found")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) answerPermission(w http.ResponseWriter, r *http.Request) {
	row := s.sessionRow(w, r)
	if row == nil {
		return
	}
	var in struct {
		Granted bool `json:"granted"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	id, err := strconv.ParseInt(r.PathValue("pid"), 10, 64)
	if err != nil {
		writeErr(w, 400, "bad permission id")
		return
	}
	wk, err := s.Sup.Worker(row.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !wk.AnswerPermission(id, in.Granted) {
		writeErr(w, 404, "no pending request with that id")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// --- checkpoints ---------------------------------------------------------------------

func (s *Server) listCheckpoints(w http.ResponseWriter, r *http.Request) {
	row := s.sessionRow(w, r)
	if row == nil {
		return
	}
	pid := row.ProjectID
	if pid == "" {
		writeJSON(w, 200, []any{})
		return
	}
	cps, err := s.App.DB.ListCheckpoints(pid)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if cps == nil {
		cps = []store.CheckpointRow{}
	}
	writeJSON(w, 200, cps)
}

func (s *Server) revert(w http.ResponseWriter, r *http.Request) {
	row := s.sessionRow(w, r)
	if row == nil {
		return
	}
	var in struct {
		StepID string `json:"step_id"`
	}
	if err := readJSON(r, &in); err != nil || in.StepID == "" {
		writeErr(w, 400, "step_id is required")
		return
	}
	if row.ProjectID == "" {
		writeErr(w, 400, "free-chat sessions have no checkpoints")
		return
	}
	cr, err := s.App.DB.FindCheckpoint(row.ProjectID, row.ID, in.StepID)
	if err != nil {
		writeErr(w, 404, "no checkpoint for that step")
		return
	}
	wk, err := s.Sup.Worker(row.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	h, err := wk.Ensure()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	names, err := h.Shadow.ChangedSinceWorktree(cr.SHA)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if err := h.Shadow.Restore(cr.SHA, names); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	wk.Hub.Publish("checkpoint_reverted", "", map[string]any{"sha": cr.SHA, "step": in.StepID, "files": names})
	writeJSON(w, 200, map[string]any{"reverted": names, "sha": cr.SHA})
}

// --- files -----------------------------------------------------------------------------

func (s *Server) handle(w http.ResponseWriter, r *http.Request) (*store.Session, *workspaceCtx) {
	row := s.sessionRow(w, r)
	if row == nil {
		return nil, nil
	}
	wk, err := s.Sup.Worker(row.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return nil, nil
	}
	h, err := wk.Ensure()
	if err != nil {
		writeErr(w, 500, err.Error())
		return nil, nil
	}
	return row, &workspaceCtx{h: h, wk: wk}
}

type workspaceCtx struct {
	h  *app.Handle
	wk *supervisor.Worker
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	_, wc := s.handle(w, r)
	if wc == nil {
		return
	}
	rel := r.URL.Query().Get("path")
	p, err := wc.h.Writer.Grants().Check("", relPath(wc.h.Cwd, rel), false)
	if err != nil {
		writeErr(w, 403, err.Error())
		return
	}
	entries, err := os.ReadDir(p.Path)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	type fi struct {
		Name  string `json:"name"`
		IsDir bool   `json:"is_dir"`
		Size  int64  `json:"size"`
	}
	out := make([]fi, 0, len(entries))
	for _, e := range entries {
		info, _ := e.Info()
		f := fi{Name: e.Name(), IsDir: e.IsDir()}
		if info != nil {
			f.Size = info.Size()
		}
		out = append(out, f)
	}
	writeJSON(w, 200, out)
}

func (s *Server) getFile(w http.ResponseWriter, r *http.Request) {
	_, wc := s.handle(w, r)
	if wc == nil {
		return
	}
	rel := r.URL.Query().Get("path")
	p, err := wc.h.Writer.Grants().Check("", relPath(wc.h.Cwd, rel), false)
	if err != nil {
		writeErr(w, 403, err.Error())
		return
	}
	data, err := os.ReadFile(p.Path)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	wc.h.Writer.MarkRead(p.Path) // editor view counts as a read (SPEC §7.2)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)
	w.Write(data)
}

func (s *Server) putFile(w http.ResponseWriter, r *http.Request) {
	_, wc := s.handle(w, r)
	if wc == nil {
		return
	}
	rel := r.URL.Query().Get("path")
	p, err := wc.h.Writer.Grants().Check("", relPath(wc.h.Cwd, rel), true)
	if err != nil {
		writeErr(w, 403, err.Error())
		return
	}
	var in struct {
		Content string `json:"content"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := wc.h.Writer.CheckRead(p.Path); err != nil {
		writeErr(w, 409, err.Error()) // must GET the file first (editor does)
		return
	}
	if err := wc.h.Writer.Mutate(r.Context(), "editor", fmt.Sprintf("before editor save(%s)", filepath.Base(p.Path)),
		[]string{p.Path}, func(t *workspace.Txn) error {
			return os.WriteFile(p.Path, []byte(in.Content), 0o644)
		}); err != nil {
		if errors.Is(err, workspace.ErrDenied) {
			writeErr(w, 403, err.Error())
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// preview serves a file in browser-viewable form (SPEC §17.2):
// text/images/pdf directly; pptx/docx/xlsx via LibreOffice → PDF when
// available; otherwise 415 with a JSON "open externally" fallback.
func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	_, wc := s.handle(w, r)
	if wc == nil {
		return
	}
	rel := r.URL.Query().Get("path")
	p := resolvePreviewPath(r.Context(), wc.h, rel)
	if p == "" {
		writeErr(w, 404, "file not found in workspace or scratchpad")
		return
	}
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		writeErr(w, 404, "not a file")
		return
	}
	ext := strings.ToLower(filepath.Ext(p))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg":
		http.ServeFile(w, r, p)
		return
	case ".pdf":
		w.Header().Set("Content-Type", "application/pdf")
		http.ServeFile(w, r, p)
		return
	case ".md", ".txt", ".csv", ".json", ".yaml", ".yml", ".log":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.ServeFile(w, r, p)
		return
	case ".pptx", ".docx", ".xlsx":
		if pdf, err := s.toPDF(r.Context(), wc.h, p, fi); err == nil {
			w.Header().Set("Content-Type", "application/pdf")
			http.ServeFile(w, r, pdf)
			return
		}
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{
			"error": "no preview available", "fallback": "download",
		})
		return
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.ServeFile(w, r, p)
	}
}

// resolvePreviewPath accepts a workspace-relative/absolute path or any
// scratchpad file (outputs/attachments), workspace winning ties.
func resolvePreviewPath(ctx context.Context, h *app.Handle, rel string) string {
	if rel == "" {
		return ""
	}
	if p, err := h.Writer.Grants().Check("", relPath(h.Cwd, rel), false); err == nil {
		if fi, serr := os.Stat(p.Path); serr == nil && !fi.IsDir() {
			return p.Path
		}
	}
	base := filepath.Base(rel)
	for _, cand := range []string{
		filepath.Join(h.Scratch, "outputs", base),
		filepath.Join(h.Scratch, "attachments", base),
		filepath.Join(h.Scratch, base),
		rel, // absolute scratchpad path as returned by run_python
	} {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand
		}
	}
	return ""
}

// toPDF converts office documents via LibreOffice (optional dependency),
// caching by path+mtime in the scratchpad previews folder.
func (s *Server) toPDF(ctx context.Context, h *app.Handle, path string, fi os.FileInfo) (string, error) {
	bin, err := exec.LookPath("soffice")
	if err != nil {
		if bin, err = exec.LookPath("libreoffice"); err != nil {
			return "", err
		}
	}
	key := sha256hex(fmt.Sprintf("%s|%d", path, fi.ModTime().UnixNano()))
	dir := filepath.Join(h.Scratch, "previews")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(dir, key+".pdf")
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "--headless", "--convert-to", "pdf", "--outdir", dir, path)
	cmd.Env = append(os.Environ(), "HOME="+dir)
	if err := cmd.Run(); err != nil {
		return "", err
	}
	// soffice writes <dir>/<base>.pdf
	generated := filepath.Join(dir, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+".pdf")
	if _, err := os.Stat(generated); err != nil {
		return "", err
	}
	if err := os.Rename(generated, out); err != nil {
		return "", err
	}
	return out, nil
}

func sha256hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// upload accepts a multipart file into the session scratchpad
// (attachments, SPEC §5.3); returns the absolute path the agent can read.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	_, wc := s.handle(w, r)
	if wc == nil {
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, 400, "multipart form with a `file` field required: "+err.Error())
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	defer f.Close()
	name := filepath.Base(hdr.Filename) // no traversal
	if name == "." || name == "/" || name == string(filepath.Separator) {
		writeErr(w, 400, "bad filename")
		return
	}
	dir := filepath.Join(wc.h.Scratch, "attachments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	dst := filepath.Join(dir, name)
	for i := 1; ; i++ { // never clobber: name-2.ext, name-3.ext, …
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			break
		}
		ext := filepath.Ext(name)
		dst = filepath.Join(dir, fmt.Sprintf("%s-%d%s", name[:len(name)-len(ext)], i, ext))
	}
	out, err := os.Create(dst)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer out.Close()
	n, err := io.Copy(out, io.LimitReader(f, 64<<20))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"path": dst, "name": filepath.Base(dst), "size": n})
}

func relPath(cwd, rel string) string {
	if rel == "" || rel == "." || rel == "/" {
		return cwd
	}
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(cwd, filepath.FromSlash(rel))
}

// listModels exposes the configured endpoint zoo for the composer's
// model chip (SPEC §5.3).
func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	type mi struct {
		Ref           string `json:"ref"`
		Endpoint      string `json:"endpoint"`
		Model         string `json:"model"`
		ContextWindow int    `json:"context_window"`
	}
	out := []mi{}
	for _, ep := range s.App.Cfg.Endpoints {
		for _, m := range ep.Models {
			out = append(out, mi{
				Ref: ep.ID + "/" + m.ID, Endpoint: ep.ID, Model: m.ID, ContextWindow: m.ContextWindow,
			})
		}
	}
	writeJSON(w, 200, map[string]any{"default": s.App.Cfg.DefaultModel, "models": out})
}

// gitStatus returns per-file VCS badges for the session's workspace
// (FilesTree badges, SPEC §5.4). {repo:false} when not a git checkout.
// --- file history (SPEC §11 UI: per-file snapshot timeline + restore) ----

// shadowFor resolves the session's shadow repo + cwd via its worker.
func (s *Server) shadowFor(w http.ResponseWriter, r *http.Request) (*app.Handle, bool) {
	row := s.sessionRow(w, r)
	if row == nil {
		return nil, false
	}
	if row.ProjectID == "" {
		writeErr(w, 400, "free-chat sessions have no checkpoints")
		return nil, false
	}
	wk, err := s.Sup.Worker(row.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return nil, false
	}
	h, err := wk.Ensure()
	if err != nil {
		writeErr(w, 500, err.Error())
		return nil, false
	}
	if h.Shadow == nil {
		writeErr(w, 400, "no checkpoint repo for this session")
		return nil, false
	}
	return h, true
}

// cleanRel validates a workspace-relative path (no escape, no absolute).
func cleanRel(w http.ResponseWriter, raw string) (string, bool) {
	p := filepath.ToSlash(strings.TrimSpace(raw))
	if p == "" || filepath.IsAbs(p) || p == "." || strings.HasPrefix(p, "../") || strings.Contains(p, "/../") {
		writeErr(w, 400, "path must be workspace-relative")
		return "", false
	}
	return p, true
}

func (s *Server) fileHistory(w http.ResponseWriter, r *http.Request) {
	h, ok := s.shadowFor(w, r)
	if !ok {
		return
	}
	rel, ok := cleanRel(w, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	hist, err := h.Shadow.FileHistory(rel, 100)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	out := []map[string]any{}
	for _, c := range hist {
		out = append(out, map[string]any{"sha": c.SHA, "label": c.Label, "when": c.When.UTC().Format(time.RFC3339)})
	}
	writeJSON(w, 200, out)
}

func (s *Server) fileAt(w http.ResponseWriter, r *http.Request) {
	h, ok := s.shadowFor(w, r)
	if !ok {
		return
	}
	rel, ok := cleanRel(w, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	sha := r.URL.Query().Get("sha")
	if sha == "" {
		writeErr(w, 400, "sha is required")
		return
	}
	b, err := h.Shadow.FileAt(sha, rel)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write(b)
}

// restoreFile rolls ONE file back to one checkpoint. SPEC §11: warn when
// user edits made after the checkpoint would be overwritten (409 unless
// force). The restore itself is a mutation → checkpointed + diff events.
func (s *Server) restoreFile(w http.ResponseWriter, r *http.Request) {
	h, ok := s.shadowFor(w, r)
	if !ok {
		return
	}
	var in struct {
		Path  string `json:"path"`
		SHA   string `json:"sha"`
		Force bool   `json:"force"`
	}
	if err := readJSON(r, &in); err != nil || in.Path == "" || in.SHA == "" {
		writeErr(w, 400, "path and sha are required")
		return
	}
	rel, ok := cleanRel(w, in.Path)
	if !ok {
		return
	}
	abs := filepath.Join(h.Cwd, filepath.FromSlash(rel))

	// user-edit detection (SPEC §11 warn-before-overwrite): the file on
	// disk differs from what the agent's last mutation left there.
	if !in.Force {
		if cur, err := os.ReadFile(abs); err == nil {
			if last, known := h.Writer.LastWritten(abs); known && last != fmt.Sprintf("%x", sha256.Sum256(cur)) {
				writeErr(w, 409, "user edits since the last checkpoint would be overwritten — retry with force")
				return
			}
		}
	}

	wk, _ := s.Sup.Worker(r.PathValue("id"))
	err := h.Writer.Mutate(r.Context(), "restore", fmt.Sprintf("restore %s to %s", rel, in.SHA[:min(8, len(in.SHA))]),
		[]string{abs}, func(*workspace.Txn) error {
			return h.Shadow.Restore(in.SHA, []string{rel})
		})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if wk != nil {
		wk.Hub.Publish("file_changed", "", map[string]any{"path": rel, "reason": "restored"})
	}
	writeJSON(w, 200, map[string]any{"restored": rel, "sha": in.SHA})
}

func (s *Server) gitStatus(w http.ResponseWriter, r *http.Request) {
	row := s.sessionRow(w, r)
	if row == nil {
		return
	}
	if row.ProjectID == "" {
		writeJSON(w, 200, map[string]any{"repo": false})
		return
	}
	proj, err := s.App.DB.GetProject(row.ProjectID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	repo, err := git.PlainOpen(proj.WorkspacePath)
	if err != nil {
		writeJSON(w, 200, map[string]any{"repo": false})
		return
	}
	wt, err := repo.Worktree()
	if err != nil {
		writeJSON(w, 200, map[string]any{"repo": false})
		return
	}
	st, err := wt.Status()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	badges := map[string]string{}
	for path, cs := range st {
		code := ""
		if cs.Staging != 0 {
			code += string(cs.Staging)
		}
		if cs.Worktree != 0 {
			code += string(cs.Worktree)
		}
		if code == "" {
			code = "M"
		}
		badges[path] = code
	}
	head := ""
	if ref, err := repo.Head(); err == nil {
		head = ref.Name().Short()
	}
	writeJSON(w, 200, map[string]any{"repo": true, "branch": head, "files": badges})
}
