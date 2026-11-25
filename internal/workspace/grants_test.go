package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func tree(t *testing.T) (cwd, scratch, outside string) {
	t.Helper()
	cwd = t.TempDir()
	scratch = t.TempDir()
	outside = t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s3cret"), 0o644); err != nil {
		t.Fatal(err)
	}
	return
}

func grants(t *testing.T, cwd, scratch string, cwdWrite bool) *Grants {
	t.Helper()
	g, err := NewGrants(cwd, scratch, cwdWrite)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGrantsAccessMatrix(t *testing.T) {
	cwd, scratch, outside := tree(t)

	g := grants(t, cwd, scratch, true) // code project w/ allow_coding
	if _, err := g.Check("", filepath.Join(cwd, "a.go"), false); err != nil {
		t.Errorf("cwd read: %v", err)
	}
	if _, err := g.Check("", filepath.Join(cwd, "a.go"), true); err != nil {
		t.Errorf("cwd write (allow_coding): %v", err)
	}
	if _, err := g.Check("", filepath.Join(scratch, "x.png"), true); err != nil {
		t.Errorf("scratch write: %v", err)
	}
	if _, err := g.Check("", filepath.Join(outside, "secret.txt"), false); err == nil {
		t.Error("outside read must be denied before grant")
	}
	if _, err := g.Check("", filepath.Join(outside, "new.txt"), true); err == nil {
		t.Error("outside write must be denied before grant")
	}

	// grant ro, then rw
	if err := g.Grant(filepath.Join(outside, "secret.txt"), ModeRO); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Check("", filepath.Join(outside, "secret.txt"), false); err != nil {
		t.Errorf("after ro grant: %v", err)
	}
	if _, err := g.Check("", filepath.Join(outside, "secret.txt"), true); err == nil {
		t.Error("ro grant must not allow write")
	}
	if err := g.Grant(filepath.Join(outside, "secret.txt"), ModeRW); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Check("", filepath.Join(outside, "secret.txt"), true); err != nil {
		t.Errorf("after rw grant: %v", err)
	}

	// no-coding code project: cwd read-only
	g2 := grants(t, cwd, scratch, false)
	if _, err := g2.Check("", filepath.Join(cwd, "a.go"), true); err == nil {
		t.Error("cwd write must be denied without allow_coding")
	}
	if _, err := g2.Check("", filepath.Join(cwd, "a.go"), false); err != nil {
		t.Errorf("cwd read still allowed: %v", err)
	}
}

func TestGrantsEscapeAttacks(t *testing.T) {
	cwd, scratch, outside := tree(t)
	g := grants(t, cwd, scratch, true)

	// ../ escape
	if _, err := g.Check("", filepath.Join(cwd, "..", "..", "etc", "passwd"), true); err == nil {
		t.Error("../ escape must be denied")
	}
	// relative ../ escape
	if _, err := g.Check("", "../outside/secret.txt", true); err == nil {
		t.Error("relative ../ escape must be denied")
	}
	// symlink escape: link inside cwd -> outside dir
	link := filepath.Join(cwd, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := g.Check("", filepath.Join("escape", "secret.txt"), true); err == nil {
		t.Error("symlink escape write must be denied")
	}
	if _, err := g.Check("", filepath.Join("escape", "secret.txt"), false); err == nil {
		t.Error("symlink escape read must be denied")
	}
	// symlink to a new file inside cwd is fine (parent resolves)
	if _, err := g.Check("", filepath.Join("escape", "..", "ok.txt"), true); err != nil {
		t.Errorf("link/.. back into cwd should be allowed: %v", err)
	}
}

func TestGrantsNewFileUnderGrantedDir(t *testing.T) {
	cwd, scratch, outside := tree(t)
	g := grants(t, cwd, scratch, true)
	sub := filepath.Join(outside, "sub")
	os.MkdirAll(sub, 0o755)
	if err := g.Grant(sub, ModeRW); err != nil {
		t.Fatal(err)
	}
	// file does not exist yet — parent symlink resolution must still match
	r, err := g.Check("", filepath.Join(sub, "deep", "new.txt"), true)
	if err == nil {
		t.Log("deep new file allowed:", r.Path) // deep dirs not created: acceptable either way
	}
	r, err = g.Check("", filepath.Join(sub, "new.txt"), true)
	if err != nil {
		t.Errorf("new file under granted dir: %v", err)
	}
}
