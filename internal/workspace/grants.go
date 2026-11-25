// Package workspace enforces SPEC §10: every path a tool touches is
// resolved (realpath + symlink-escape check) and checked against the
// session grant table before any FS access. The WorkspaceWriter is the one
// mutation path: permissions → checkpoint → write → diff → event, in a
// critical section that interrupts cannot preempt (SPEC §7.4).
package workspace

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// ErrDenied reports a permission denial (message is model-facing).
var ErrDenied = errors.New("denied")

// Mode is a grant level.
type Mode string

// Grant modes (SPEC §6.2 permissions.mode).
const (
	ModeRO Mode = "ro"
	ModeRW Mode = "rw"
)

// Grants is the session-scoped access table (SPEC §10.1).
type Grants struct {
	mu       sync.RWMutex
	cwd      string // realpath
	scratch  string // realpath
	cwdWrite bool   // Code projects: allow_coding; Work workspaces: true
	extra    map[string]Mode
}

// NewGrants builds the table from session roots.
func NewGrants(cwd, scratch string, cwdWrite bool) (*Grants, error) {
	c, err := canonical(cwd)
	if err != nil {
		return nil, fmt.Errorf("workspace: cwd: %w", err)
	}
	s, err := canonical(scratch)
	if err != nil {
		return nil, fmt.Errorf("workspace: scratchpad: %w", err)
	}
	return &Grants{cwd: c, scratch: s, cwdWrite: cwdWrite, extra: map[string]Mode{}}, nil
}

// CWD exposes the canonical working directory.
func (g *Grants) CWD() string { return g.cwd }

// Scratch exposes the canonical scratchpad directory.
func (g *Grants) Scratch() string { return g.scratch }

// Grant adds a session-scoped grant for a path (deepest-wins: a grant on a
// subtree does not widen one on the file itself).
func (g *Grants) Grant(path string, mode Mode) error {
	p, err := canonical(path)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if mode == ModeRW || g.extra[p] != ModeRW {
		g.extra[p] = mode
	}
	return nil
}

// LoadGrants bulk-adds persisted grants (resume).
func (g *Grants) LoadGrants(m map[string]string) {
	for p, m2 := range m {
		_ = g.Grant(p, Mode(m2))
	}
}

// Resolved is the outcome of a successful check.
type Resolved struct {
	Path string // canonical target
	Mode Mode
}

// Check resolves path (relative paths from base, default cwd) and verifies
// the access is allowed. write=true demands rw.
func (g *Grants) Check(base, path string, write bool) (Resolved, error) {
	p, err := g.resolve(base, path)
	if err != nil {
		return Resolved{}, err
	}
	want := ModeRO
	if write {
		want = ModeRW
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	switch {
	case within(g.cwd, p):
		if !write || g.cwdWrite {
			m := ModeRO
			if g.cwdWrite {
				m = ModeRW
			}
			return Resolved{Path: p, Mode: m}, nil
		}
	case within(g.scratch, p):
		return Resolved{Path: p, Mode: ModeRW}, nil
	}
	if m := g.matchLocked(p); m != "" && (m == ModeRW || want == ModeRO) {
		return Resolved{Path: p, Mode: m}, nil
	}
	if write {
		return Resolved{}, fmt.Errorf("%w: no read-write access to %q — call request_read_write_access first", ErrDenied, display(path))
	}
	return Resolved{}, fmt.Errorf("%w: no read access to %q — call request_readonly_access first", ErrDenied, display(path))
}

// matchLocked finds the most specific extra grant covering p.
func (g *Grants) matchLocked(p string) Mode {
	best, bestLen := Mode(""), 0
	for root, m := range g.extra {
		if within(root, p) && len(root) > bestLen {
			best, bestLen = m, len(root)
		}
	}
	return best
}

// resolve makes path absolute and canonical without following a final
// symlink that does not exist yet (new files), and rejects symlink escape:
// the nearest existing ancestor is realpath'd, so `link -> /etc` cannot
// smuggle writes outside granted roots.
func (g *Grants) resolve(base, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%w: empty path", ErrDenied)
	}
	if !filepath.IsAbs(path) {
		if base == "" {
			base = g.cwd
		}
		path = filepath.Join(base, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	// canonicalize the deepest existing ancestor, re-attach the rest
	rest := []string{}
	cur := abs
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				real = filepath.Join(real, rest[i])
			}
			return filepath.Clean(real), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil // nothing exists on this branch
		}
		rest = append(rest, filepath.Base(cur))
		cur = parent
	}
}

// canonical = abs + EvalSymlinks (for roots that must exist).
func canonical(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(real), nil
}

// within reports whether root contains path (root itself counts).
func within(root, p string) bool {
	if root == "" {
		return false
	}
	r := strings.TrimSuffix(root, string(filepath.Separator))
	if r == "" {
		r = string(filepath.Separator)
	}
	return p == r || strings.HasPrefix(p, r+string(filepath.Separator))
}

func display(p string) string { return filepath.ToSlash(p) }
