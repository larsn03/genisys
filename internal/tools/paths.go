package tools

import (
	"os"
	"path/filepath"
	"strings"
)

// Resolve maps a tool-supplied path to an absolute one: absolute paths pass
// through cleaned, relative paths resolve from cwd (SPEC §9.1). P2 layers
// permission grants on top of this.
func Resolve(cwd, p string) string {
	p = expandTilde(p)
	if p == "" {
		return cwd
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(cwd, p))
}

func expandTilde(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
