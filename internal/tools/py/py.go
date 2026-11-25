// Package py implements run_python (SPEC §9.3, §17.1): sandboxed Python
// via the embedded CPython-WASI guest, /inputs /outputs contract,
// paperworks preinstalled, outputs copied back to the scratchpad.
package py

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/larsn03/genisys/internal/agent/events"
	"github.com/larsn03/genisys/internal/sandbox"
	"github.com/larsn03/genisys/internal/tools"
)

//go:embed schema.json
var schemaJSON string

// Tool implements tools.Tool.
type Tool struct{}

// Name is the model-visible tool name.
func (Tool) Name() string { return "run_python" }

// Description tells the model about the contract and paperworks.
func (Tool) Description() string {
	return `Run Python code in a sandbox (no network, no system access).
Read staged files from /inputs/<name>; write deliverables to /outputs/<name> — files written there are copied into the session scratchpad and listed in the result.
Preinstalled: paperworks (from ppt_builder import Deck; from docx_redline import redline_docx, read_docx_text; from xlsx_helpers import write_xlsx, read_xlsx) plus the Python standard library (zipfile, json, csv, math…). Use paperworks for .pptx/.docx deliverables, not python-pptx.
Use print() for findings; use /outputs files for documents.`
}

// Schema returns the JSON schema for arguments.
func (Tool) Schema() json.RawMessage { return json.RawMessage(schemaJSON) }

type args struct {
	Code       string   `json:"code"`
	StageFiles []string `json:"stage_files"`
	TimeoutMs  int      `json:"timeout_ms"`
}

// Execute runs the guest (SPEC §17.1 Tier-1; interrupt = hard close).
func (Tool) Execute(ctx context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a args
	if err := json.Unmarshal(raw, &a); err != nil {
		return errRes("run_python: bad args: " + err.Error())
	}
	if strings.TrimSpace(a.Code) == "" {
		return errRes("run_python: code is required")
	}
	if w.RuntimeDir == "" {
		return errRes("run_python: sandbox runtime not configured")
	}
	guest, err := sandbox.EnsureGuest(w.RuntimeDir)
	if err != nil {
		return errRes("run_python: guest setup: " + err.Error())
	}

	// stage inputs (scratchpad or workspace paths → /inputs/<base>)
	inputs := ""
	if len(a.StageFiles) > 0 {
		dir, err := os.MkdirTemp("", "genisys-pyin-")
		if err != nil {
			return errRes("run_python: " + err.Error())
		}
		defer os.RemoveAll(dir)
		for _, spec := range a.StageFiles {
			src, err := resolveAny(w, spec)
			if err != nil {
				return errRes("run_python: stage: " + err.Error())
			}
			if err := copyFile(src, filepath.Join(dir, filepath.Base(src))); err != nil {
				return errRes("run_python: stage: " + err.Error())
			}
		}
		inputs = dir
	}

	// outputs land in the scratchpad under a per-call folder
	callDir := filepath.Join(w.Scratchpad, "outputs",
		fmt.Sprintf("%s-%d", orDefault(w.StepID, "step"), time.Now().UnixNano()%100000))
	if err := os.MkdirAll(callDir, 0o755); err != nil {
		return errRes("run_python: " + err.Error())
	}

	timeout := time.Duration(a.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if timeout > 5*time.Minute {
		timeout = 5 * time.Minute
	}

	res, err := sandbox.Run(ctx, sandbox.Request{
		GuestDir: guest, Code: a.Code, InputsDir: inputs,
		OutputsDir: callDir, Timeout: timeout,
	})
	if w.Emit != nil {
		w.Emit.Emit(events.Event{Type: "tool_output", StepID: w.StepID, Payload: map[string]any{
			"exit": res.ExitCode, "stdout": clip(res.Stdout, 4000),
		}})
	}
	if err != nil { // runtime failure (not guest error)
		return errRes("run_python: " + err.Error())
	}

	var b strings.Builder
	if res.Stdout != "" {
		b.WriteString(res.Stdout)
	}
	if res.Stderr != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("[stderr]\n" + res.Stderr)
	}
	if len(res.Outputs) > 0 {
		b.WriteString("\n[output files — in your scratchpad, readable via read_file_lines; deliver with deliver_file_to_user]\n")
		for _, o := range res.Outputs {
			b.WriteString("- " + filepath.Join(callDir, filepath.FromSlash(o)) + "\n")
		}
	}
	if res.ExitCode != 0 || res.TimedOut {
		out := b.String()
		if out == "" {
			out = fmt.Sprintf("(no output, exit code %d)", res.ExitCode)
		}
		return errRes(out)
	}
	if b.Len() == 0 {
		b.WriteString("(ran successfully, no output)")
	}
	return tools.Result{Content: b.String()}
}

// resolveAny accepts a workspace-relative, absolute, or scratchpad path.
func resolveAny(w *tools.World, spec string) (string, error) {
	cands := []string{spec}
	if !filepath.IsAbs(spec) {
		cands = append(cands,
			filepath.Join(w.Cwd, spec),
			filepath.Join(w.Scratchpad, spec),
			filepath.Join(w.Scratchpad, "attachments", filepath.Base(spec)),
			filepath.Join(w.Scratchpad, "outputs", filepath.Base(spec)),
		)
	}
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("%q not found (tried workspace and scratchpad)", spec)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func errRes(content string) tools.Result { return tools.Result{Content: content, IsError: true} }

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
