// run.go — `genisys run` (interactive REPL) and `genisys revert`
// (checkpoint rollback). The REPL wires app.Open + App.OpenSession and
// renders the event spine in the terminal; permission cards suspend the
// REPL until the next input line answers them.

package main

import (
	"bufio"
	"context"
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
	"github.com/larsn03/genisys/internal/app"
	"github.com/larsn03/genisys/internal/model"
	"github.com/larsn03/genisys/internal/store"
	"github.com/larsn03/genisys/internal/workspace"
)

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
	fs.StringVar(&o.resume, "resume", "", "resume: session id, or 'latest' for this workspace")
	fs.BoolVar(&o.newSession, "new", false, "start a fresh session (default)")
	fs.BoolVar(&o.quiet, "quiet", false, "hide harness progress lines")
	fs.Parse(args)
	return runREPL(o)
}

func runREPL(o runOpts) error {
	a, err := app.Open(o.cfgPath, o.dbFlag)
	if err != nil {
		return err
	}
	defer a.Close()

	ref := o.modelRef
	if ref == "" {
		ref = a.Cfg.DefaultModel
	}
	if ref == "" {
		return fmt.Errorf("no model selected: pass --model endpoint/id or set default_model in %s", o.cfgPath)
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
	proj, err := a.ProjectFor(cwd)
	if err != nil {
		return err
	}

	var row *store.Session
	if o.resume != "" {
		if o.resume == "latest" {
			row, err = a.DB.LatestSession(proj.ID)
		} else {
			row, err = a.DB.GetSession(o.resume)
		}
		if err != nil {
			return fmt.Errorf("resume: %w", err)
		}
		if o.modelRef == "" && row.ModelRef != "" {
			ref = row.ModelRef
		}
	} else {
		row = &store.Session{ProjectID: proj.ID, ModelRef: ref, Persona: "coder"}
		if err := a.DB.CreateSession(row); err != nil {
			return err
		}
	}

	// stdin reader + permission channel (SPEC §9.7: the loop suspends on
	// pending cards; the next input line is the answer)
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

	h, err := a.OpenSession(row, proj, app.SessionOpts{
		Emit: sink, Ask: ask, ModelRef: ref, ToolsMode: o.toolsMode, Cwd: cwd,
	})
	if err != nil {
		return err
	}
	defer h.Close()
	as := h.Sess

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

	mode := "native"
	if _, m, err := a.ModelClient(ref); err == nil {
		mode = m
	}
	fmt.Printf("genisys %s — workspace: %s — model: %s (tools: %s) — session: %s\n", version, cwd, ref, mode, row.ID)
	if o.resume != "" {
		fmt.Printf("resumed %d persisted messages\n", len(as.Messages()))
	}
	fmt.Println("Type a task. While the agent works: /steer <text> interrupts, /queue <text> waits. /quit exits.")

	defMode := agent.InterjectionMode(a.Cfg.Agent.Interjection.DefaultMode)
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
		if row.Title == "New session" {
			_ = a.DB.SetSessionTitle(row.ID, titleOf(text))
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
			fmt.Printf("  id %s  project %s\n", row.ID, proj.ID)
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
	_ = a.DB.SetSessionStatus(row.ID, store.StatusIdle)
	return nil
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

func cutPrefixArg(line, cmd string) (string, bool) {
	if line == cmd {
		return "", true
	}
	if strings.HasPrefix(line, cmd+" ") {
		return strings.TrimSpace(strings.TrimPrefix(line, cmd+" ")), true
	}
	return "", false
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

	a, err := app.Open(*cfgPath, *dbFlag)
	if err != nil {
		return err
	}
	defer a.Close()

	wd, _ := os.Getwd()
	cwd := *cwdFlag
	if cwd == "" {
		cwd = wd
	}
	proj, err := a.ProjectFor(cwd)
	if err != nil {
		return err
	}
	cps, err := a.DB.ListCheckpoints(proj.ID)
	if err != nil {
		return err
	}
	if *list || *step == "" {
		if len(cps) == 0 {
			fmt.Println("no checkpoints for", cwd)
			return nil
		}
		for _, c := range cps {
			fmt.Printf("%s  %-8s  %-10s %s\n", c.CreatedAt, short(c.SessionID, 8), c.StepID, c.Label)
		}
		if *step == "" {
			fmt.Println("\nspecify --step <id> to revert (newest matching checkpoint is used)")
		}
		return nil
	}
	cr, err := a.DB.FindCheckpoint(proj.ID, *sessionID, *step)
	if err != nil {
		return fmt.Errorf("no checkpoint for step %q: %w (try `genisys revert --list`)", *step, err)
	}
	cp, err := workspace.OpenShadow(filepath.Join(a.Root, "projects", proj.ID, "checkpoints"), proj.WorkspacePath)
	if err != nil {
		return err
	}
	// the step's file set = diff between its checkpoint and the state
	// after the step: the NEXT checkpoint if one exists, else HEAD, else
	// the live worktree (SPEC §11: exactly that step's files).
	var names []string
	for i, c := range cps { // cps are newest-first
		if c.ID == cr.ID && i > 0 {
			names, err = cp.DiffNames(cr.SHA, cps[i-1].SHA)
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
	fmt.Printf("reverted %d file(s) to checkpoint %s (%s):\n", len(names), short(cr.SHA, 8), cr.Label)
	for _, n := range names {
		fmt.Println("  ", n)
	}
	return nil
}

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
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
