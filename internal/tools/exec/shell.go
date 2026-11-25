// Package exec implements shell_command (SPEC §9.3): user shell, process
// groups, timeouts, and the "summarize the output" note (SPEC §7.3).
package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"time"

	"github.com/larsn03/genisys/internal/tools"
	"github.com/larsn03/genisys/internal/workspace"
)

// dangerousRe: rm -rf / (and variants), sudo, pipe-to-shell (SPEC §9.3).
var dangerousRe = regexp.MustCompile(`(^|\s)sudo\s|rm\s+-[a-zA-Z]*[rRf]+[^|;]*\s/(\s|$|")|\|\s*(ba|z|da)?sh(\s|$)`)

type Shell struct{}

func (Shell) Name() string { return "shell_command" }
func (Shell) Description() string {
	return "Run a command in the user's default shell. The command inherits the current user environment; relative workdir values resolve from the session working directory."
}
func (Shell) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"command":{"type":"string","description":"Command to run in the resolved shell."},
"workdir":{"type":["string","null"],"description":"Working directory. Relative paths resolve from the working directory."},
"timeout_ms":{"type":["integer","null"],"description":"Timeout in milliseconds. Default: 10000; cap 300000."}},
"required":["command"],"additionalProperties":false}`)
}

const hardTimeoutMs = 300000

func (Shell) Execute(ctx context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Command   string  `json:"command"`
		Workdir   *string `json:"workdir"`
		TimeoutMs *int    `json:"timeout_ms"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil {
		return tools.Result{Content: "invalid_json: " + err.Error(), IsError: true}
	}
	if a.Command == "" {
		return tools.Result{Content: "invalid_args: command is required", IsError: true}
	}
	timeout := w.ShellTimeout
	if timeout <= 0 {
		timeout = 10000
	}
	if a.TimeoutMs != nil && *a.TimeoutMs > 0 {
		timeout = *a.TimeoutMs
	}
	if timeout > hardTimeoutMs {
		timeout = hardTimeoutMs
	}

	// SPEC §9.3/§10.2: deny-list heuristics raise a permission card before
	// the command runs (cwd-pinned execution + OS sandbox are the rest).
	if w.Perms != nil && dangerousRe.MatchString(a.Command) {
		if err := w.Perms.Request(ctx, w.Cwd, workspace.ModeRW,
			"shell command needs elevated permission: "+a.Command); err != nil {
			return tools.Result{Content: "error: " + err.Error(), IsError: true}
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	defer cancel()

	name, args := shellFor(a.Command)
	cmd := exec.CommandContext(runCtx, name, args...)
	cmd.Dir = w.Cwd
	if a.Workdir != nil && *a.Workdir != "" {
		d, err := w.Access(*a.Workdir, false)
		if err != nil {
			return tools.Result{Content: "error: " + err.Error(), IsError: true}
		}
		cmd.Dir = d
	}
	killGroup := setupProc(cmd)
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = 2 * time.Second

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	text := out.String()

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		return tools.Result{
			Content: fmt.Sprintf("timeout after %dms (partial output follows)\n%s", timeout, text) + tools.NoteForShell,
			IsError: true,
		}
	case ctx.Err() != nil:
		// interrupt (SPEC §7.4): the loop injects the synthetic result;
		// surface partial output anyway.
		return tools.Result{Content: "[interrupted — partial output]\n" + text, IsError: true}
	}

	exit := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		exit = ee.ExitCode()
	} else if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	res := fmt.Sprintf("exit code: %d\n\n%s", exit, text)
	if text != "" {
		res += tools.NoteForShell
	}
	return tools.Result{Content: res, IsError: exit != 0}
}

func shellFor(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", command}
	}
	if _, err := exec.LookPath("bash"); err == nil {
		return "bash", []string{"-lc", command}
	}
	return "sh", []string{"-lc", command}
}
