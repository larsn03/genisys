// wasm.go — Tier-1 runner: wazero + CPython-WASI (SPEC §17.1).
// In-process, no network, real in-memory contract: /inputs (ro),
// /outputs (rw), /tmp (scratch). Interrupt = ctx cancel + hard close.

package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

// Request is one guest execution.
type Request struct {
	GuestDir   string        // EnsureGuest() result
	Code       string        // python source (run via -c)
	InputsDir  string        // mounted read-only at /inputs ("" = none)
	OutputsDir string        // mounted rw at /outputs (created if missing)
	ExtraPath  string        // prepended to PYTHONPATH (e.g. /lib/paperworks)
	Timeout    time.Duration // default 60s
	MaxOutput  int           // stdout/stderr capture cap (default 1 MiB)
}

// Result is the guest outcome.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Outputs  []string // file names created under /outputs
	TimedOut bool
}

// Run executes Request in the WASM guest.
func Run(ctx context.Context, req Request) (*Result, error) {
	if req.Timeout == 0 {
		req.Timeout = 60 * time.Second
	}
	if req.MaxOutput == 0 {
		req.MaxOutput = 1 << 20
	}
	if req.OutputsDir == "" {
		return nil, fmt.Errorf("sandbox: OutputsDir required")
	}
	if err := os.MkdirAll(req.OutputsDir, 0o755); err != nil {
		return nil, err
	}
	wasmBytes, err := os.ReadFile(filepath.Join(req.GuestDir, "python.wasm"))
	if err != nil {
		return nil, fmt.Errorf("sandbox guest: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	// JIT + WithCloseOnContextDone: wazero itself terminates running
	// calls when ctx is cancelled (documented guarantee for
	// ExportedFunction.Call) — no external CloseWithExitCode watchdog,
	// which races the wazevo engine's teardown under -race.
	rtCfg := wazero.NewRuntimeConfig().WithCloseOnContextDone(true)
	if cache, cerr := wazero.NewCompilationCacheWithDir(filepath.Join(req.GuestDir, ".wazero-cache")); cerr == nil {
		rtCfg = rtCfg.WithCompilationCache(cache) // fast warm starts
	}
	rt := wazero.NewRuntimeWithConfig(runCtx, rtCfg)
	if rt == nil {
		return nil, fmt.Errorf("sandbox: runtime init failed")
	}
	defer func() { _ = rt.Close(runCtx) }()
	wasi_snapshot_preview1.MustInstantiate(runCtx, rt)

	// cancellation model: instantiate WITHOUT running, then Call _start
	// explicitly — WithCloseOnContextDone guarantees the Call
	// terminates on ctx cancellation (interrupt or timeout).

	fsCfg := wazero.NewFSConfig().
		WithReadOnlyDirMount(req.GuestDir, "/python").
		WithReadOnlyDirMount(filepath.Join(req.GuestDir, "paperworks"), "/lib/paperworks")
	if req.InputsDir != "" {
		fsCfg = fsCfg.WithReadOnlyDirMount(req.InputsDir, "/inputs")
	}
	tmpDir, err := os.MkdirTemp("", "genisys-pytmp-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	fsCfg = fsCfg.WithDirMount(req.OutputsDir, "/outputs").WithDirMount(tmpDir, "/tmp")

	pythonpath := "/lib/paperworks"
	if req.ExtraPath != "" {
		pythonpath = req.ExtraPath + ":" + pythonpath
	}
	out := &capWriter{cap: req.MaxOutput}
	errw := &capWriter{cap: req.MaxOutput}
	modCfg := wazero.NewModuleConfig().
		WithName("python").
		WithArgs("python", "-c", req.Code).
		WithEnv("PYTHONHOME", "/python").
		WithEnv("PYTHONPATH", pythonpath).
		WithEnv("PYTHONDONTWRITEBYTECODE", "1").
		WithEnv("PYTHONWASM", "1").
		WithEnv("HOME", "/tmp").
		WithEnv("TMPDIR", "/tmp").
		WithStartFunctions(). // nothing auto-runs; _start called explicitly
		WithFSConfig(fsCfg).
		WithStdout(out).WithStderr(errw)

	mod, instErr := rt.InstantiateWithConfig(runCtx, wasmBytes, modCfg)
	if instErr == nil {
		start := mod.ExportedFunction("_start")
		if start == nil {
			instErr = fmt.Errorf("sandbox: guest has no _start")
		} else {
			_, instErr = start.Call(runCtx)
		}
	}

	res := &Result{Stdout: out.String(), Stderr: errw.String()}
	var exitErr *sys.ExitError
	switch {
	case runCtx.Err() != nil:
		// cancelled or timed out mid-execution (close-on-context-done
		// may unwind with a nil error, an ExitError, or a closed error)
		res.TimedOut = true
		res.ExitCode = -1
		instErr = nil
	case instErr == nil: // returned without proc_exit (treated as 0)
	case errorsAsExit(instErr, &exitErr):
		res.ExitCode = int(exitErr.ExitCode())
	}
	if res.TimedOut && ctx.Err() == nil { // timeout, not interrupt
		res.Stderr += fmt.Sprintf("\n[python timed out after %s]", req.Timeout)
	}
	if instErr != nil && !res.TimedOut {
		return res, fmt.Errorf("sandbox: %w", instErr)
	}
	if res.TimedOut && res.Stderr == "" {
		res.Stderr = "[python interrupted]"
	}

	_ = filepath.WalkDir(req.OutputsDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if rel, rerr := filepath.Rel(req.OutputsDir, p); rerr == nil {
				res.Outputs = append(res.Outputs, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	return res, nil
}

// capWriter buffers up to cap bytes, then drops silently (counting).
type capWriter struct {
	buf bytes.Buffer
	cap int
	n   int
}

func (w *capWriter) Write(p []byte) (int, error) {
	w.n += len(p)
	if w.buf.Len() < w.cap {
		w.buf.Write(p[:min(len(p), w.cap-w.buf.Len())])
	}
	return len(p), nil
}

func (w *capWriter) String() string {
	if w.n > w.buf.Len() {
		return w.buf.String() + fmt.Sprintf("\n[… %d more bytes truncated]", w.n-w.buf.Len())
	}
	return w.buf.String()
}

func errorsAsExit(err error, target **sys.ExitError) bool {
	if e, ok := err.(*sys.ExitError); ok {
		*target = e
		return true
	}
	return false
}
