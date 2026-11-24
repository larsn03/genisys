//go:build windows

package exec

import "os/exec"

// setupProc on Windows: no process groups; CommandContext kills the process
// itself (documented limitation, IMPL §16 R7).
func setupProc(_ *exec.Cmd) func(*exec.Cmd) error {
	return func(c *exec.Cmd) error {
		if c.Process == nil {
			return nil
		}
		return c.Process.Kill()
	}
}
