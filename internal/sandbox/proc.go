// proc.go — Tier-2 runner (SPEC §17.1): host python3 inside an OS
// sandbox (bwrap / nsjail / sandbox-exec / docker, autodetected), same
// /inputs /outputs contract. Used when code needs packages the WASM
// guest lacks; capability report drives the choice.

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"
)

// Capabilities reports what this machine can run.
type Capabilities struct {
	WASM        bool   `json:"wasm"`  // Tier-1: always true (embedded guest)
	Tier2       string `json:"tier2"` // bwrap | nsjail | sandbox-exec | docker | ""
	Tier2Python bool   `json:"tier2_python"`
	LibreOffice bool   `json:"libreoffice"` // §17.2 office previews
}

// Detect probes PATH for the Tier-2 stack (cheap, no execution).
func Detect() Capabilities {
	c := Capabilities{WASM: true}
	_, c.Tier2Python = lookPath("python3")
	for _, tool := range []string{"bwrap", "nsjail", "sandbox-exec", "docker"} {
		if _, ok := lookPath(tool); ok {
			c.Tier2 = tool
			break
		}
	}
	if _, ok := lookPath("soffice"); !ok {
		_, ok = lookPath("libreoffice")
		c.LibreOffice = ok
	} else {
		c.LibreOffice = true
	}
	return c
}

func lookPath(name string) (string, bool) {
	p, err := exec.LookPath(name)
	return p, err == nil
}

// ProcRequest mirrors Request for the Tier-2 path.
type ProcRequest struct {
	Code       string
	InputsDir  string
	OutputsDir string
	Timeout    time.Duration
	MaxOutput  int
}

// RunProc executes code with host python3 under the detected sandbox.
// Returns ErrNoTier2 when nothing usable exists.
func RunProc(ctx context.Context, req ProcRequest) (*Result, error) {
	cap := Detect()
	if cap.Tier2 == "" || !cap.Tier2Python {
		return nil, fmt.Errorf("no Tier-2 runner (need python3 + bwrap/nsjail/sandbox-exec/docker)")
	}
	if req.Timeout == 0 {
		req.Timeout = 60 * time.Second
	}
	if req.MaxOutput == 0 {
		req.MaxOutput = 1 << 20
	}
	if err := os.MkdirAll(req.OutputsDir, 0o755); err != nil {
		return nil, err
	}

	// paperworks for the host interpreter too
	pwDir, err := writePaperworksTemp()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(pwDir)
	tmpDir, err := os.MkdirTemp("", "genisys-proctmp-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	pythonpath := pwDir
	if req.InputsDir == "" {
		req.InputsDir = tmpDir // mount target must exist
	}

	cctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	cmd, err := tier2Command(cctx, cap.Tier2, req, pythonpath)
	if err != nil {
		return nil, err
	}
	out, errw := &capWriter{cap: req.MaxOutput}, &capWriter{cap: req.MaxOutput}
	cmd.Stdout, cmd.Stderr = out, errw
	runErr := cmd.Run()

	res := &Result{Stdout: out.String(), Stderr: errw.String()}
	if cctx.Err() != nil {
		res.TimedOut = true
		res.ExitCode = -1
	} else if runErr != nil {
		var ee *exec.ExitError
		if ok := asExit(runErr, &ee); ok {
			res.ExitCode = ee.ExitCode()
		} else {
			return res, runErr
		}
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

func tier2Command(ctx context.Context, tool string, req ProcRequest, pythonpath string) (*exec.Cmd, error) {
	env := []string{"PYTHONPATH=" + pythonpath, "PYTHONDONTWRITEBYTECODE=1", "HOME=/tmp", "TMPDIR=/tmp", "PATH=/usr/bin:/bin"}
	switch tool {
	case "bwrap":
		// read-only host root, network off; contract dirs live under a
		// writable tmpfs (bwrap cannot create mount points on the
		// read-only root) — Tier-2 guests see /tmp/inputs /tmp/outputs,
		// plus INPUTS_DIR/OUTPUTS_DIR env.
		args := []string{
			"--ro-bind", "/", "/",
			"--tmpfs", "/tmp",
			"--bind", req.OutputsDir, "/tmp/outputs",
			"--ro-bind", req.InputsDir, "/tmp/inputs",
			"--ro-bind", pythonpath, "/tmp/paperworks",
			"--dev", "/dev", "--proc", "/proc",
			"--unshare-net", "--die-with-parent", "--",
			"python3", "-c", req.Code,
		}
		cmd := exec.CommandContext(ctx, "bwrap", args...)
		cmd.Env = append(slices.Clone(env), "PYTHONPATH=/tmp/paperworks",
			"INPUTS_DIR=/tmp/inputs", "OUTPUTS_DIR=/tmp/outputs", "TMPDIR=/tmp")
		return cmd, nil
	case "nsjail":
		cmd := exec.CommandContext(ctx, "nsjail", "--", "python3", "-c", req.Code)
		cmd.Env = append(slices.Clone(env), "INPUTS="+req.InputsDir, "OUTPUTS="+req.OutputsDir)
		return cmd, nil // config file left to operators; capability-gated
	case "sandbox-exec":
		profile := `(version 1)(allow default)(deny network*)`
		cmd := exec.CommandContext(ctx, "sandbox-exec", "-p", profile, "python3", "-c", req.Code)
		cmd.Env = append(slices.Clone(env), "INPUTS="+req.InputsDir, "OUTPUTS="+req.OutputsDir, "PYTHONPATH="+pythonpath)
		return cmd, nil
	case "docker":
		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-i", "--network", "none",
			"-v", req.InputsDir+":/inputs:ro", "-v", req.OutputsDir+":/outputs",
			"-e", "PYTHONPATH="+pythonpath,
			"python:3.12-slim", "python", "-c", req.Code)
		return cmd, nil
	}
	return nil, fmt.Errorf("unsupported tier2 %q", tool)
}

func writePaperworksTemp() (string, error) {
	files, err := PaperworksFiles()
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "genisys-paperworks-")
	if err != nil {
		return "", err
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func asExit(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}
