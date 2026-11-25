// checkpoint.go — shadow git repos (SPEC §11): a git dir under
// ~/.genisys/projects/<id>/checkpoints with the workspace as work tree, so
// the user's own .git is never touched. Implemented with go-git (no git
// binary dependency).
package workspace

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage"
	"github.com/go-git/go-git/v5/storage/filesystem"
)

// ShadowRepo is one checkpoint repo (git-dir separate from work-tree).
type ShadowRepo struct {
	r        *git.Repository
	workTree string
	gitDir   string // path of the git-dir (for loose-object prune)
}

// OpenShadow opens (initializing if needed) a shadow repo whose git data
// lives in gitDir and whose work tree is workTree.
func OpenShadow(gitDir, workTree string) (*ShadowRepo, error) {
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(workTree, 0o755); err != nil {
		return nil, err
	}
	// git-dir/work-tree split WITHOUT touching the worktree: go-git's Init
	// would drop a .git pointer file into the work tree (clobbering a user
	// repo), so we initialize the storer by hand and Open with an explicit
	// worktree filesystem — the --git-dir/--work-tree equivalent.
	st := filesystem.NewStorage(osfs.New(filepath.Join(gitDir, ".git")), cache.NewObjectLRUDefault())
	if _, err := os.Stat(filepath.Join(gitDir, ".git", "HEAD")); err != nil {
		if err := initShadowStorer(st); err != nil {
			return nil, fmt.Errorf("workspace: shadow init: %w", err)
		}
	}
	r, err := git.Open(st, osfs.New(workTree))
	if err != nil {
		return nil, err
	}
	return &ShadowRepo{r: r, workTree: workTree, gitDir: filepath.Join(gitDir, ".git")}, nil
}

// initShadowStorer creates the git-dir skeleton, HEAD -> master and a
// minimal config (mirrors InitWithOptions minus the dot-git file write).
func initShadowStorer(st storage.Storer) error {
	if init, ok := st.(storer.Initializer); ok {
		if err := init.Init(); err != nil {
			return err
		}
	}
	if err := st.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.Master)); err != nil {
		return err
	}
	cfg := config.NewConfig()
	return st.SetConfig(cfg)
}

// Commit stages relPaths and commits them; message = "step · label".
// Returns the commit sha. An empty diff yields a fresh empty commit so the
// ledger always has a restore point (cheap: no blob writes).
func (s *ShadowRepo) Commit(stepID, label string, relPaths []string) (string, error) {
	w, err := s.r.Worktree()
	if err != nil {
		return "", err
	}
	for _, p := range relPaths {
		if !within(s.workTree, filepath.Join(s.workTree, p)) {
			continue // absolute/outside paths: skip staging
		}
		if _, err := w.Add(p); err != nil {
			// deletions must be staged too
			if _, err2 := w.Remove(p); err2 != nil {
				continue
			}
		}
	}
	msg := stepID + " · " + label
	if stepID == "" {
		msg = label
	}
	sha, err := w.Commit(msg, &git.CommitOptions{
		Author:            &object.Signature{Name: "genisys", Email: "genisys@local", When: time.Now()},
		AllowEmptyCommits: true, // every mutation step gets a restore point
	})
	if err != nil {
		return "", err
	}
	return sha.String(), nil
}

// Restore writes relPaths back to their state at sha (files absent from
// the commit are deleted).
func (s *ShadowRepo) Restore(sha string, relPaths []string) error {
	h := plumbing.NewHash(sha)
	commit, err := s.r.CommitObject(h)
	if err != nil {
		return fmt.Errorf("checkpoint %s: %w", sha[:8], err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return err
	}
	for _, p := range relPaths {
		full := filepath.Join(s.workTree, p)
		f, ferr := tree.File(filepath.ToSlash(p))
		if errors.Is(ferr, object.ErrDirectoryNotFound) || errors.Is(ferr, object.ErrFileNotFound) {
			if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		} else if ferr != nil {
			return ferr
		}
		blob, err := s.r.BlobObject(f.Hash)
		if err != nil {
			return err
		}
		rc, err := blob.Reader()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if m, err := f.Mode.ToOSFileMode(); err == nil && m&0o111 != 0 {
			mode = 0o755
		}
		if err := os.WriteFile(full, b, mode); err != nil {
			return err
		}
	}
	return nil
}

// FileHistory lists commits touching relPath, newest first (Files tab
// "Roll back to checkpoint…").
type CommitInfo struct {
	SHA   string
	Label string
	When  time.Time
}

// FileHistory walks history for one path.
func (s *ShadowRepo) FileHistory(relPath string, limit int) ([]CommitInfo, error) {
	name := filepath.ToSlash(relPath)
	it, err := s.r.Log(&git.LogOptions{FileName: &name})
	if err != nil {
		return nil, err
	}
	defer it.Close()
	var out []CommitInfo
	err = it.ForEach(func(c *object.Commit) error {
		if len(out) < limit {
			out = append(out, CommitInfo{SHA: c.Hash.String(), Label: c.Message, When: c.Author.When})
		}
		return nil
	})
	return out, err
}

// GC keeps the newest `keep` commits (SPEC §11: 200) by deleting the
// loose commit objects beyond the window. Trees/blobs stay (they are
// shared by kept chains; deleting them would need full reachability and
// buys little). Restoring a pruned sha then fails cleanly with "object
// not found", which is exactly the ledger semantics we want.
func (s *ShadowRepo) GC(keep int) error {
	head, err := s.r.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return nil
		}
		return err
	}
	iter, err := s.r.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		return err
	}
	defer iter.Close()
	var doomed []*object.Commit
	i := 0
	err = iter.ForEach(func(c *object.Commit) error {
		if i >= keep {
			doomed = append(doomed, c)
		}
		i++
		return nil
	})
	if err != nil {
		return err
	}
	for _, c := range doomed {
		if err := s.removeLoose(c.Hash); err != nil {
			return err
		}
	}
	return nil
}

// removeLoose deletes one loose object file (git's loose layout:
// objects/<2>/<38>).
func (s *ShadowRepo) removeLoose(h plumbing.Hash) error {
	p := filepath.Join(s.gitDir, "objects", h.String()[:2], h.String()[2:])
	err := os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// HeadSHA returns the current branch tip ("" on unborn HEAD).
func (s *ShadowRepo) HeadSHA() string {
	h, err := s.r.Head()
	if err != nil {
		return ""
	}
	return h.Hash().String()
}

// DiffNames lists the paths that differ between two commits — the file
// set `genisys revert` restores (SPEC §11: "revert step reverts exactly
// that step's files": diff the step's checkpoint against the NEXT one).
func (s *ShadowRepo) DiffNames(shaA, shaB string) ([]string, error) {
	ta, err := s.treeOf(shaA)
	if err != nil {
		return nil, err
	}
	tb, err := s.treeOf(shaB)
	if err != nil {
		return nil, err
	}
	changes, err := object.DiffTree(ta, tb)
	if err != nil {
		return nil, err
	}
	return changeNames(changes), nil
}

// ChangedSinceWorktree lists worktree changes relative to sha's commit —
// used to revert the LAST step, whose effects were never committed.
func (s *ShadowRepo) ChangedSinceWorktree(sha string) ([]string, error) {
	if h := s.HeadSHA(); h != "" && h != sha {
		// sha is not the tip: diff sha → HEAD covers the step
		names, err := s.DiffNames(sha, h)
		if err != nil {
			return nil, err
		}
		return names, nil
	}
	w, err := s.r.Worktree()
	if err != nil {
		return nil, err
	}
	st, err := w.Status()
	if err != nil {
		return nil, err
	}
	var out []string
	for p := range st {
		out = append(out, filepath.ToSlash(p))
	}
	return out, nil
}

// FileAt reads one file's content at one commit (file-history preview,
// SPEC §11 UI: "Roll back to checkpoint…" lists per-file snapshots).
func (s *ShadowRepo) FileAt(sha, relPath string) ([]byte, error) {
	tree, err := s.treeOf(sha)
	if err != nil {
		return nil, err
	}
	f, err := tree.File(filepath.ToSlash(relPath))
	if err != nil {
		return nil, fmt.Errorf("workspace: no snapshot of %q at that checkpoint", relPath)
	}
	rc, err := f.Reader()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func (s *ShadowRepo) treeOf(sha string) (*object.Tree, error) {
	c, err := s.r.CommitObject(plumbing.NewHash(sha))
	if err != nil {
		return nil, fmt.Errorf("checkpoint %s: %w", sha[:min8(len(sha))], err)
	}
	return c.Tree()
}

func changeNames(changes object.Changes) []string {
	var out []string
	seen := map[string]bool{}
	for _, ch := range changes {
		name := ch.To.Name
		if name == "" {
			name = ch.From.Name
		}
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

func min8(n int) int {
	if n < 8 {
		return n
	}
	return 8
}
