// rest.go — CRUD + control endpoints (SPEC §19).

package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/go-git/go-git/v5"

	"github.com/larsn03/genisys/internal/agent"
	"github.com/larsn03/genisys/internal/apiconfig"
	"github.com/larsn03/genisys/internal/app"
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
	m.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"running": s.Sup.RunningSessions(), "queued": s.Sup.Queued(), "cap": s.Sup.Cap,
		})
	})

	// config (settings UI): GET raw YAML; PATCH dotted keys comment-preservingly
	m.HandleFunc("GET /api/config", s.getConfig)
	m.HandleFunc("PATCH /api/config", s.patchConfig)

	m.HandleFunc("GET /api/projects", s.listProjects)
	m.HandleFunc("POST /api/projects", s.createProject)
	m.HandleFunc("GET /api/projects/{id}", s.getProject)
	m.HandleFunc("DELETE /api/projects/{id}", s.deleteProject)

	m.HandleFunc("GET /api/sessions", s.listSessions)
	m.HandleFunc("POST /api/sessions", s.createSession)
	m.HandleFunc("GET /api/sessions/{id}", s.getSession)
	m.HandleFunc("PATCH /api/sessions/{id}", s.patchSession)
	m.HandleFunc("DELETE /api/sessions/{id}", s.archiveSession)

	m.HandleFunc("GET /api/sessions/{id}/messages", s.sessionMessages)
	m.HandleFunc("GET /api/sessions/{id}/events", s.sessionEvents)
	m.HandleFunc("POST /api/sessions/{id}/prompt", s.prompt)
	m.HandleFunc("POST /api/sessions/{id}/stop", s.stop)
	m.HandleFunc("GET /api/sessions/{id}/interjections", s.listInterjections)
	m.HandleFunc("POST /api/sessions/{id}/interjections/{ij}/{op}", s.interjectionOp)
	m.HandleFunc("POST /api/sessions/{id}/permissions/{pid}", s.answerPermission)
	m.HandleFunc("GET /api/sessions/{id}/checkpoints", s.listCheckpoints)
	m.HandleFunc("GET /api/sessions/{id}/git-status", s.gitStatus)
	m.HandleFunc("POST /api/sessions/{id}/revert", s.revert)
	m.HandleFunc("GET /api/sessions/{id}/files", s.listFiles)
	m.HandleFunc("GET /api/sessions/{id}/file", s.getFile)
	m.HandleFunc("PUT /api/sessions/{id}/file", s.putFile)

	m.HandleFunc("POST /api/sessions/{id}/upload", s.upload)
	m.HandleFunc("GET /ws", s.ws)
	m.HandleFunc("GET /", s.serveUI)
}

// Version is stamped by cmd at startup.
var Version = "dev"

// --- config -------------------------------------------------------------------

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

func (s *Server) patchConfig(w http.ResponseWriter, r *http.Request) {
	var kv map[string]string
	if err := readJSON(r, &kv); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	path := s.ConfigPath
	err := apiconfig.Update(path, func(doc *yaml.Node) error {
		for k, v := range kv {
			if err := apiconfig.Set(doc, strings.Split(k, "."), v); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// reload so the running server picks up web/agent changes
	if cfg, err := apiconfig.LoadApp(path); err == nil {
		issues := apiconfig.ValidateApp(cfg, path)
		if len(issues) > 0 {
			writeErr(w, http.StatusBadRequest, apiconfig.Issues(issues).Error())
			return
		}
		s.App.Cfg = cfg
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
		s.App.DB.SetSessionTitle(row.ID, *in.Title)
	}
	if in.Pinned != nil {
		s.App.DB.SetSessionPinned(row.ID, *in.Pinned)
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
	if err := s.App.DB.SetSessionStatus(row.ID, store.StatusArchived); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
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

func (s *Server) prompt(w http.ResponseWriter, r *http.Request) {
	row := s.sessionRow(w, r)
	if row == nil {
		return
	}
	var in struct {
		Text string `json:"text"`
		Mode string `json:"mode"` // queue | interrupt (default queue)
	}
	if err := readJSON(r, &in); err != nil || in.Text == "" {
		writeErr(w, 400, "text is required")
		return
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
