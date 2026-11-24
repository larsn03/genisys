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
	"github.com/larsn03/genisys/internal/model"
	"github.com/larsn03/genisys/internal/tools"
	exectool "github.com/larsn03/genisys/internal/tools/exec"
	"github.com/larsn03/genisys/internal/tools/fileio"
	"github.com/larsn03/genisys/internal/tools/search"
)

func defaultRegistry() *tools.Registry {
	r := tools.NewRegistry()
	r.Add(fileio.ReadLines{})
	r.Add(fileio.ReadChars{})
	r.Add(fileio.ListDir{})
	r.Add(search.SearchLine{})
	r.Add(search.FindFiles{})
	r.Add(exectool.Shell{})
	return r
}

func runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", filepath.Join(apiconfig.ExpandPath("~"), ".genisys", "config.yaml"), "app config path")
	modelRef := fs.String("model", "", "model reference endpoint/id (default: default_model)")
	cwd := fs.String("cwd", "", "workspace directory (default: current dir)")
	toolsMode := fs.String("tools-mode", "", "native|xml|none (default: from model profile)")
	quiet := fs.Bool("quiet", false, "hide harness progress lines")
	fs.Parse(args)

	cfg, err := apiconfig.LoadApp(*cfgPath)
	if err != nil {
		return err
	}
	ref := *modelRef
	if ref == "" {
		ref = cfg.DefaultModel
	}
	if ref == "" {
		return fmt.Errorf("no model selected: pass --model endpoint/id or set default_model in %s", *cfgPath)
	}
	ep, prof, ok := cfg.ModelRef(ref)
	if !ok {
		return fmt.Errorf("model %q not found in %s (run `genisys config show`)", ref, *cfgPath)
	}
	key := os.Getenv(ep.APIKeyEnv)
	if key == "" {
		key = ep.APIKey
	}
	client, err := model.New(ep.BaseURL, key, model.WithQuirks(prof.Quirks...))
	if err != nil {
		return err
	}
	mode := *toolsMode
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
	ws := *cwd
	if ws == "" {
		ws = wd
	}
	scratch, err := os.MkdirTemp("", "genisys-run-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)

	interj := agent.NewInterjector()
	var sink events.Sink = printerSink{}
	if *quiet {
		sink = events.Discard
	}
	sess := agent.NewSession(agent.Deps{
		Client: client, Model: ref, Tools: defaultRegistry(),
		ToolsMode: mode, MaxSteps: cfg.Agent.MaxSteps,
		Cwd: ws, Scratchpad: scratch, Events: sink, Interj: interj,
	})

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
				stop() // second-purpose: exit
				return
			}
		}
	}()

	fmt.Printf("genisys %s — workspace: %s — model: %s (tools: %s)\n", version, ws, ref, mode)
	fmt.Println("Type a task. While the agent works: /steer <text> interrupts, /queue <text> waits. /quit exits.")

	defMode := agent.InterjectionMode(cfg.Agent.Interjection.DefaultMode)
	if defMode != agent.ModeInterrupt {
		defMode = agent.ModeQueue
	}

	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("you> ")
		if !in.Scan() {
			return nil
		}
		line := strings.TrimSpace(in.Text())
		switch {
		case line == "":
			continue
		case line == "/quit" || line == "/exit":
			mu.Lock()
			if running && cancelRun != nil {
				cancelRun()
			}
			mu.Unlock()
			wg.Wait()
			return nil
		case line == "/pending":
			for _, ij := range interj.Pending() {
				fmt.Printf("  [%s] %s: %.60q\n", ij.Mode, ij.ID, ij.Text)
			}
			continue
		}

		mu.Lock()
		busy := running
		if !busy {
			running = true // claim synchronously: fast stdin must not
		} // launch two runs on one session
		mu.Unlock()

		text, forced := line, agent.InterjectionMode("")
		if rest, ok := cutPrefixArg(line, "/steer"); ok {
			text, forced = rest, agent.ModeInterrupt
		} else if rest, ok := cutPrefixArg(line, "/queue"); ok {
			text, forced = rest, agent.ModeQueue
		}
		if forced == "" && !busy {
			forced = agent.ModeQueue // plain prompt while idle = normal turn
		}

		if !busy && forced != agent.ModeQueue {
			// /steer while idle: just run it as a normal turn
			forced = agent.ModeQueue
		}

		if busy {
			ij := interj.Push(text, forced)
			if forced == agent.ModeInterrupt {
				fmt.Println("(interrupt sent)", ij.ID)
			} else {
				fmt.Println("(queued — will be read at the next boundary)", ij.ID)
			}
			continue
		}

		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			runCtx, cancel := context.WithCancel(ctx)
			mu.Lock()
			cancelRun = cancel
			mu.Unlock()
			_, err := sess.Run(runCtx, text)
			cancel()
			mu.Lock()
			running, cancelRun = false, nil
			mu.Unlock()
			if err != nil && !errors.Is(err, context.Canceled) {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
		}(text)
	}
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
	case events.Usage:
		if u, ok := e.Payload.(model.Usage); ok {
			fmt.Fprintf(os.Stderr, "  tokens: prompt %d completion %d\n", u.PromptTokens, u.CompletionTokens)
		}
	case events.TextDelta:
		fmt.Printf("\n%v\n", p["text"])
	}
}
