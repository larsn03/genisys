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
	p = strings.TrimSpace(p)
	if p == "" {
		return cwd
	}
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(cwd, p))
}
