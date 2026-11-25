// Package app is the wiring layer shared by every front-end (CLI REPL,
// `genisys serve`, tests): it opens the state db, resolves projects and
// sessions, and assembles agent.Deps (store, transcript, grants, shadow
// checkpoints, writer, permission broker, tool registry).
package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/larsn03/genisys/internal/agent"
	"github.com/larsn03/genisys/internal/agent/events"
	"github.com/larsn03/genisys/internal/apiconfig"
	"github.com/larsn03/genisys/internal/model"
	"github.com/larsn03/genisys/internal/store"
	"github.com/larsn03/genisys/internal/tools"
	exectool "github.com/larsn03/genisys/internal/tools/exec"
	"github.com/larsn03/genisys/internal/tools/fileio"
	"github.com/larsn03/genisys/internal/tools/mutate"
	"github.com/larsn03/genisys/internal/tools/perms"
	"github.com/larsn03/genisys/internal/tools/search"
	"github.com/larsn03/genisys/internal/tools/ui"
	"github.com/larsn03/genisys/internal/tools/web"
	"github.com/larsn03/genisys/internal/websearch"
	"github.com/larsn03/genisys/internal/workspace"
)

// App owns the process-level singletons.
type App struct {
	Cfg  *apiconfig.Config
	DB   *store.DB
	Root string // ~/.genisys
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
	return &App{Cfg: cfg, DB: db, Root: filepath.Dir(dbPath)}, nil
}

// Close releases the db handle.
func (a *App) Close() error { return a.DB.Close() }

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
	if withUI {
		for _, t := range ui.Tools() {
			r.Add(t)
		}
	}
	if c := a.Websearch(); c != nil {
		r.Add(web.Search{C: c})
	}
	return r
}

// Websearch builds the SearXNG client or nil (SPEC §9.5).
func (a *App) Websearch() *websearch.Client {
	if !a.Cfg.Web.SearchEnabled || a.Cfg.Web.Provider != "searxng" || a.Cfg.Web.Searxng.BaseURL == "" {
		return nil
	}
	c, err := websearch.New(websearch.Config{
		BaseURL:          a.Cfg.Web.Searxng.BaseURL,
		Language:         a.Cfg.Web.Searxng.Language,
		Categories:       a.Cfg.Web.Searxng.Categories,
		TimeoutMs:        a.Cfg.Web.Searxng.TimeoutMs,
		APIKeyEnv:        a.Cfg.Web.Searxng.APIKeyEnv,
		AllowInsecureTLS: a.Cfg.Web.Searxng.AllowInsecureTLS,
	})
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

	deps := agent.Deps{
		Client: client, Model: ref, Tools: o.Registry, ToolsMode: mode,
		MaxSteps: a.Cfg.Agent.MaxSteps, ShellTimeout: a.Cfg.Agent.ShellTimeoutMs,
		Cwd: cwd, Scratchpad: scratch, Events: o.Emit,
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

func projectID(p *store.Project) string {
	if p == nil {
		return ""
	}
	return p.ID
}
