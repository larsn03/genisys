//go:build !windows

package exec

import (
	"os/exec"
	"syscall"
)

// setupProc puts the child in its own process group and returns a killer
// that takes down the whole group (SPEC §9.3: servers spawned by the agent
// die with the step).
func setupProc(cmd *exec.Cmd) func(*exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return func(c *exec.Cmd) error {
		if c.Process == nil {
			return nil
		}
		pgid, err := syscall.Getpgid(c.Process.Pid)
		if err != nil {
			return c.Process.Kill()
		}
		// SIGTERM first, the Cancel/WaitDelay machinery escalates.
		return syscall.Kill(-pgid, syscall.SIGKILL)
	}
}
