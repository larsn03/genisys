// run.go — `genisys run` (interactive REPL) and `genisys revert`
// (checkpoint rollback). Wires config → store → workspace (grants, writer,
// shadow checkpoints, permission broker) → agent session, and renders the
// event spine in the terminal.
package main

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

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
	"github.com/larsn03/genisys/internal/tools/web"
	"github.com/larsn03/genisys/internal/websearch"
	"github.com/larsn03/genisys/internal/workspace"
)

// fullRegistry assembles the tool surface; web_search is registered only
// when enabled AND configured (SPEC §9.5: hidden from schema otherwise).
func fullRegistry(cfg *apiconfig.Config) *tools.Registry {
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
	if c := websearchFromConfig(cfg); c != nil {
		r.Add(web.Search{C: c})
	}
	return r
}

// websearchFromConfig builds the SearXNG client or nil.
func websearchFromConfig(cfg *apiconfig.Config) *websearch.Client {
	if !cfg.Web.SearchEnabled || cfg.Web.Provider != "searxng" || cfg.Web.Searxng.BaseURL == "" {
		return nil
	}
	c, err := websearch.New(websearch.Config{
		BaseURL:          cfg.Web.Searxng.BaseURL,
		Language:         cfg.Web.Searxng.Language,
		Categories:       cfg.Web.Searxng.Categories,
		TimeoutMs:        cfg.Web.Searxng.TimeoutMs,
		APIKeyEnv:        cfg.Web.Searxng.APIKeyEnv,
		AllowInsecureTLS: cfg.Web.Searxng.AllowInsecureTLS,
	})
	if err != nil {
		return nil
	}
	return c
}

// genRoot is ~/.genisys (overridable with --db's directory for tests).
func genRoot(dbPath string) string {
	if dbPath != "" {
		return filepath.Dir(dbPath)
	}
	return filepath.Join(apiconfig.ExpandPath("~"), ".genisys")
}

func defaultDBPath() string {
	return filepath.Join(apiconfig.ExpandPath("~"), ".genisys", "genisys.db")
}

type boot struct {
	cfg  *apiconfig.Config
	db   *store.DB
	root string
}

func bootDB(dbFlag, cfgPath string) (*boot, error) {
	cfg, err := apiconfig.LoadApp(cfgPath)
	if errors.Is(err, os.ErrNotExist) {
		// revert/serve need only the state db; config is optional there
		cfg = (&apiconfig.Config{}).WithDefaults()
	} else if err != nil {
		return nil, err
	}
	root := genRoot(dbFlag)
	dbPath := dbFlag
	if dbPath == "" {
		dbPath = defaultDBPath()
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		return nil, err
	}
	// SPEC §23 boot step: orphan sessions → error, pending permissions →
	// denied (nobody survived the crash).
	if _, err := db.MarkOrphansInterrupted(); err != nil {
		db.Close()
		return nil, err
	}
	if err := db.DenyAllPending(); err != nil {
		db.Close()
		return nil, err
	}
	return &boot{cfg: cfg, db: db, root: root}, err
}

// projectFor returns (creating on demand) the project bound to a folder.
func projectFor(db *store.DB, cwd string) (*store.Project, error) {
	clean := filepath.Clean(cwd)
	if p, err := db.GetProjectByWorkspacePath(clean); err == nil {
		return p, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	p := &store.Project{Name: filepath.Base(clean), Kind: "code", WorkspacePath: clean, AllowCoding: true}
	if err := db.CreateProject(p); err != nil {
		return nil, err
	}
	return p, nil
}

func workspaceHash(cwd string) string {
	s := sha1.Sum([]byte(filepath.Clean(cwd)))
	return hex.EncodeToString(s[:8])
}

type runOpts struct {
	cfgPath, dbFlag, modelRef, cwd, toolsMode, resume string
	quiet, newSession                                 bool
}

func runCmd(args []string) error {
	var o runOpts
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	fs.StringVar(&o.cfgPath, "config", filepath.Join(apiconfig.ExpandPath("~"), ".genisys", "config.yaml"), "app config path")
	fs.StringVar(&o.dbFlag, "db", "", "state db path (default ~/.genisys/genisys.db)")
	fs.StringVar(&o.modelRef, "model", "", "model reference endpoint/id (default: default_model)")
	fs.StringVar(&o.cwd, "cwd", "", "workspace directory (default: current dir)")
	fs.StringVar(&o.toolsMode, "tools-mode", "", "native|xml|none (default: from model profile)")
	fs.StringVar(&o.resume, "resume", "", "resume session id (bare --resume: most recent for this workspace)")
	fs.BoolVar(&o.newSession, "new", false, "start a fresh session (default)")
	fs.BoolVar(&o.quiet, "quiet", false, "hide harness progress lines")
	fs.Parse(args)
	return runREPL(o)
}

func runREPL(o runOpts) error {
	b, err := bootDB(o.dbFlag, o.cfgPath)
	if err != nil {
		return err
	}
	defer b.db.Close()

	ref := o.modelRef
	if ref == "" {
		ref = b.cfg.DefaultModel
	}
	if ref == "" {
		return fmt.Errorf("no model selected: pass --model endpoint/id or set default_model in %s", o.cfgPath)
	}
	ep, prof, ok := b.cfg.ModelRef(ref)
	if !ok {
		return fmt.Errorf("model %q not found in %s (run `genisys config show`)", ref, o.cfgPath)
	}
	key := os.Getenv(ep.APIKeyEnv)
	if key == "" {
		key = ep.APIKey
	}
	client, err := model.New(ep.BaseURL, key, model.WithQuirks(prof.Quirks...))
	if err != nil {
		return err
	}
	mode := o.toolsMode
	if mode == "" {
		mode = prof.Supports.Tools
	}
	if mode == "" {
		mode = "native"
	}

	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	cwd := o.cwd
	if cwd == "" {
		cwd = wd
	}
	if cwd, err = filepath.Abs(cwd); err != nil {
		return err
	}
	proj, err := projectFor(b.db, cwd)
	if err != nil {
		return err
	}

	// session: resume or create
	var sess *store.Session
	if o.resume != "" {
		if o.resume == "latest" {
			sess, err = b.db.LatestSession(proj.ID)
		} else {
			sess, err = b.db.GetSession(o.resume)
		}
		if err != nil {
			return fmt.Errorf("resume: %w", err)
		}
		if sess.ModelRef != "" && o.modelRef == "" {
			ref = sess.ModelRef
			if ep2, prof2, ok2 := b.cfg.ModelRef(ref); ok2 {
				ep, prof = ep2, prof2
				key = os.Getenv(ep.APIKeyEnv)
				if key == "" {
					key = ep.APIKey
				}
				client, err = model.New(ep.BaseURL, key, model.WithQuirks(prof.Quirks...))
				if err != nil {
					return err
				}
				if o.toolsMode == "" {
					mode = prof.Supports.Tools
					if mode == "" {
						mode = "native"
					}
				}
			}
		}
	} else {
		sess = &store.Session{ProjectID: proj.ID, ModelRef: ref, Persona: "coder"}
		if err := b.db.CreateSession(sess); err != nil {
			return err
		}
	}

	// scratchpad (SPEC §6.1: ~/.genisys/scratchpad/<session-id>/)
	scratch := filepath.Join(b.root, "scratchpad", sess.ID)
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return err
	}

	// workspace stack: grants → shadow checkpoints → writer → broker
	grants, err := workspace.NewGrants(cwd, scratch, true)
	if err != nil {
		return err
	}
	if granted, err := b.db.GrantedPaths(sess.ID); err == nil {
		grants.LoadGrants(granted) // session-scoped grants survive resume
	}
	shadowDir := filepath.Join(b.root, "projects", proj.ID, "checkpoints")
	cp, err := workspace.OpenShadow(shadowDir, cwd)
	if err != nil {
		return err
	}

	// stdin reader + permission channel (loop suspends on pending cards)
	linesCh := readStdin()
	type permAsk struct {
		req   workspace.PermRequest
		reply chan bool
	}
	permCh := make(chan permAsk, 4)
	ask := func(ctx context.Context, req workspace.PermRequest) bool {
		reply := make(chan bool, 1)
		select {
		case permCh <- permAsk{req, reply}:
		case <-ctx.Done():
			return false
		}
		select {
		case ok := <-reply:
			return ok
		case <-ctx.Done():
			return false
		}
	}

	var sink events.Sink = printerSink{}
	if o.quiet {
		sink = events.Discard
	}
	writer := workspace.NewWriter(workspace.WriterOpts{
		Grants: grants, Cp: cp, DB: b.db, ProjectID: proj.ID, SessionID: sess.ID, Emit: sink,
	})
	broker := workspace.NewBroker(grants, b.db, sess.ID, 0, ask, sink)

	tpath := agent.TranscriptPath(filepath.Join(b.root, "projects", proj.ID), "", sess.ID)
	tr, err := store.OpenTranscript(tpath)
	if err != nil {
		return err
	}
	defer tr.Close()

	deps := agent.Deps{
		Client: client, Model: ref, Tools: fullRegistry(b.cfg), ToolsMode: mode,
		MaxSteps: b.cfg.Agent.MaxSteps, ShellTimeout: b.cfg.Agent.ShellTimeoutMs,
		Cwd: cwd, Scratchpad: scratch, Events: sink,
		SessionID: sess.ID, Transcript: tr, DB: b.db, ProjectID: proj.ID,
		Writer: writer, Perms: broker,
	}
	var as *agent.Session
	if o.resume != "" {
		as, err = agent.Resume(deps)
		if err != nil {
			return err
		}
	} else {
		as = agent.NewSession(deps)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var (
		mu        sync.Mutex
		running   bool
		cancelRun context.CancelFunc
		wg        sync.WaitGroup
	)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT)
	go func() {
		for range sigCh {
			mu.Lock()
			run, cancel := running, cancelRun
			mu.Unlock()
			if run && cancel != nil {
				fmt.Fprintln(os.Stderr, "\n(stopping current task — queued input stays pending)")
				cancel()
			} else {
				stop()
				return
			}
		}
	}()

	fmt.Printf("genisys %s — workspace: %s — model: %s (tools: %s) — session: %s\n", version, cwd, ref, mode, sess.ID)
	if o.resume != "" {
		fmt.Printf("resumed %d persisted messages from %s\n", len(as.Messages()), tpath)
	}
	fmt.Println("Type a task. While the agent works: /steer <text> interrupts, /queue <text> waits. /quit exits.")

	defMode := agent.InterjectionMode(b.cfg.Agent.Interjection.DefaultMode)
	if defMode != agent.ModeInterrupt {
		defMode = agent.ModeQueue
	}

	send := func(text string, forced agent.InterjectionMode) {
		mu.Lock()
		busy := running
		if !busy {
			running = true
		}
		mu.Unlock()
		if busy {
			ij := as.Interjections().Push(text, forced)
			if forced == agent.ModeInterrupt {
				fmt.Println("(interrupt sent)", ij.ID)
			} else {
				fmt.Println("(queued — will be read at the next boundary)", ij.ID)
			}
			return
		}
		if sess.Title == "New session" {
			_ = b.db.SetSessionTitle(sess.ID, titleOf(text))
		}
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			runCtx, cancel := context.WithCancel(ctx)
			mu.Lock()
			cancelRun = cancel
			mu.Unlock()
			_, err := as.Run(runCtx, text)
			cancel()
			mu.Lock()
			running, cancelRun = false, nil
			mu.Unlock()
			if err != nil && !errors.Is(err, context.Canceled) {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
		}(text)
	}

repl:
	for {
		var (
			line string
			ok   bool
		)
		fmt.Print("you> ")
		select {
		case <-ctx.Done():
			return nil
		case pa := <-permCh:
			// SPEC §9.7: the loop suspends here; the next input line is
			// the answer. Timeout/interrupt handled by the broker.
			fmt.Fprintf(os.Stderr, "\n⚿ permission: %s access to %s\n  reason: %s\nallow? [y/N] ",
				pa.req.Mode, pa.req.Path, orNone(pa.req.Reason))
			ans := false
			l, ok := <-linesCh
			if ok {
				t := strings.ToLower(strings.TrimSpace(l))
				ans = t == "y" || t == "yes" || t == "allow"
			}
			pa.reply <- ans
			continue
		case line, ok = <-linesCh:
			if !ok {
				break repl
			}
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case line == "/quit" || line == "/exit":
			break repl
		case line == "/pending":
			for _, ij := range as.Interjections().Pending() {
				fmt.Printf("  [%s] %s: %.60q\n", ij.Mode, ij.ID, ij.Text)
			}
			continue
		case line == "/session":
			fmt.Printf("  id %s  project %s  transcript %s\n", sess.ID, proj.ID, tpath)
			continue
		}

		text, forced := line, agent.InterjectionMode("")
		if rest, isCmd := cutPrefixArg(line, "/steer"); isCmd {
			text, forced = rest, agent.ModeInterrupt
		} else if rest, isCmd := cutPrefixArg(line, "/queue"); isCmd {
			text, forced = rest, agent.ModeQueue
		}
		if forced == "" {
			mu.Lock()
			busy := running
			mu.Unlock()
			if busy {
				forced = defMode
			} else {
				forced = agent.ModeQueue // plain prompt while idle = normal turn
			}
		}
		send(text, forced)
	}
	mu.Lock()
	if running && cancelRun != nil {
		cancelRun()
	}
	mu.Unlock()
	wg.Wait()
	_ = b.db.SetSessionStatus(sess.ID, store.StatusIdle)
	return nil
}

func cutPrefixArg(line, cmd string) (string, bool) {
	if line == cmd {
		return "", true
	}
	if strings.HasPrefix(line, cmd+" ") {
		return strings.TrimSpace(strings.TrimPrefix(line, cmd+" ")), true
	}
	return "", false
}

func titleOf(text string) string {
	t := strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	if len(t) > 48 {
		t = t[:48] + "…"
	}
	return t
}

func orNone(s string) string {
	if s == "" {
		return "(no reason given)"
	}
	return s
}

func readStdin() chan string {
	ch := make(chan string)
	go func() {
		defer close(ch)
		in := bufio.NewScanner(os.Stdin)
		in.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for in.Scan() {
			ch <- in.Text()
		}
	}()
	return ch
}

// --- revert ------------------------------------------------------------------

func revertCmd(args []string) error {
	fs := flag.NewFlagSet("revert", flag.ExitOnError)
	dbFlag := fs.String("db", "", "state db path")
	cfgPath := fs.String("config", filepath.Join(apiconfig.ExpandPath("~"), ".genisys", "config.yaml"), "app config path")
	cwdFlag := fs.String("cwd", "", "workspace (default: current dir)")
	step := fs.String("step", "", "step id to revert (reverts exactly that step's files)")
	sessionID := fs.String("session", "", "restrict to a session id")
	list := fs.Bool("list", false, "list checkpoints instead of reverting")
	fs.Parse(args)

	b, err := bootDB(*dbFlag, *cfgPath)
	if err != nil {
		return err
	}
	defer b.db.Close()

	wd, _ := os.Getwd()
	cwd := *cwdFlag
	if cwd == "" {
		cwd = wd
	}
	proj, err := projectFor(b.db, cwd)
	if err != nil {
		return err
	}
	cps, err := b.db.ListCheckpoints(proj.ID)
	if err != nil {
		return err
	}
	if *list || *step == "" {
		if len(cps) == 0 {
			fmt.Println("no checkpoints for", cwd)
			return nil
		}
		for _, c := range cps {
			fmt.Printf("%s  %-8s  %-10s %s\n", c.CreatedAt, c.SessionID[:min(8, len(c.SessionID))], c.StepID, c.Label)
		}
		if *step == "" {
			fmt.Println("\nspecify --step <id> to revert (newest matching checkpoint is used)")
		}
		return nil
	}
	cr, err := b.db.FindCheckpoint(proj.ID, *sessionID, *step)
	if err != nil {
		return fmt.Errorf("no checkpoint for step %q: %w (try `genisys revert --list`)", *step, err)
	}
	cp, err := workspace.OpenShadow(filepath.Join(b.root, "projects", proj.ID, "checkpoints"), proj.WorkspacePath)
	if err != nil {
		return err
	}
	// the step's file set = diff between its checkpoint and the state
	// after the step: the NEXT checkpoint if one exists, else HEAD, else
	// the live worktree (SPEC §11: exactly that step's files).
	var names []string
	for i, c := range cps { // cps are newest-first
		if c.ID == cr.ID && i > 0 {
			names, err = cp.DiffNames(cr.SHA, cps[i-1].SHA) // next checkpoint
			if err != nil {
				return err
			}
			break
		}
	}
	if len(names) == 0 {
		names, err = cp.ChangedSinceWorktree(cr.SHA)
		if err != nil {
			return err
		}
	}
	if len(names) == 0 {
		return fmt.Errorf("nothing to revert at step %q", *step)
	}
	if err := cp.Restore(cr.SHA, names); err != nil {
		return err
	}
	fmt.Printf("reverted %d file(s) to checkpoint %s (%s):\n", len(names), cr.SHA[:min(8, len(cr.SHA))], cr.Label)
	for _, n := range names {
		fmt.Println("  ", n)
	}
	return nil
}

// printerSink renders the event spine for the terminal (SPEC §21 consumer).
type printerSink struct{}

func (printerSink) Emit(e events.Event) {
	p, _ := e.Payload.(map[string]any)
	switch e.Type {
	case events.StepStarted:
		fmt.Fprintf(os.Stderr, "· step %s\n", p["step"])
	case events.ToolStarted:
		fmt.Fprintf(os.Stderr, "  → %s %s\n", p["tool"], p["args"])
	case events.ToolFinished:
		mark := "✗"
		if p["ok"] == true {
			mark = "✓"
		}
		fmt.Fprintf(os.Stderr, "  %s %v\n", mark, p["summary"])
	case events.InterruptStarted:
		fmt.Fprintln(os.Stderr, "  ⚡ interrupt")
	case events.InterjectionDelivered:
		fmt.Fprintf(os.Stderr, "  ⇩ interjection delivered (%v)\n", p["boundary"])
	case events.FileChanged:
		fmt.Fprintf(os.Stderr, "  %s %v\n", fileMark(p["change"]), p["path"])
	case events.CheckpointCreated:
		fmt.Fprintf(os.Stderr, "  ◉ checkpoint %v\n", p["label"])
	case events.Usage:
		if u, ok := e.Payload.(model.Usage); ok {
			fmt.Fprintf(os.Stderr, "  tokens: prompt %d completion %d\n", u.PromptTokens, u.CompletionTokens)
		}
	case events.TextDelta:
		fmt.Printf("\n%v\n", p["text"])
	}
}

func fileMark(change any) string {
	switch change {
	case "created":
		return "+"
	case "deleted":
		return "−"
	default:
		return "~"
	}
}
