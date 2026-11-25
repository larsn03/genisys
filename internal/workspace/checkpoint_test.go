package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShadowCheckpointRestore(t *testing.T) {
	ws := t.TempDir()
	gitDir := t.TempDir() // shadow lives outside the workspace entirely

	// user's own .git must stay untouched
	userGit := filepath.Join(ws, ".git")
	os.MkdirAll(userGit, 0o755)
	os.WriteFile(filepath.Join(userGit, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)

	os.WriteFile(filepath.Join(ws, "index.html"), []byte("<h1>before</h1>\n"), 0o644)
	os.MkdirAll(filepath.Join(ws, "src"), 0o755)
	os.WriteFile(filepath.Join(ws, "src", "app.js"), []byte("v1\n"), 0o644)

	cp, err := OpenShadow(gitDir, ws)
	if err != nil {
		t.Fatal(err)
	}
	sha1, err := cp.Commit("step_1", "before edit_file_tool(index.html)", []string{"index.html", "src/app.js"})
	if err != nil || sha1 == "" {
		t.Fatalf("commit1: %q %v", sha1, err)
	}
	// agent mutates
	os.WriteFile(filepath.Join(ws, "index.html"), []byte("<h1>after</h1>\n"), 0o644)
	os.WriteFile(filepath.Join(ws, "src", "app.js"), []byte("v2\n"), 0o644)
	sha2, err := cp.Commit("step_2", "before replace_file(app.js)", []string{"index.html", "src/app.js"})
	if err != nil || sha2 == "" {
		t.Fatalf("commit2: %q %v", sha2, err)
	}
	if sha1 == sha2 {
		t.Error("second commit identical — staging missed the change")
	}

	// revert step_1's files to pre-step_1 state
	if err := cp.Restore(sha1, []string{"index.html", "src/app.js"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(ws, "index.html"))
	if string(b) != "<h1>before</h1>\n" {
		t.Errorf("revert html = %q", b)
	}
	b, _ = os.ReadFile(filepath.Join(ws, "src", "app.js"))
	if string(b) != "v1\n" {
		t.Errorf("revert js = %q", b)
	}

	// user .git untouched
	h, _ := os.ReadFile(filepath.Join(userGit, "HEAD"))
	if string(h) != "ref: refs/heads/main\n" {
		t.Errorf("user .git clobbered: %q", h)
	}
	// shadow git dir is separate
	if _, err := os.Stat(filepath.Join(gitDir, ".git")); err != nil {
		t.Error("shadow git dir missing")
	}

	// file history for the Files tab
	hist, err := cp.FileHistory("index.html", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) < 1 || hist[0].SHA == "" {
		t.Errorf("history = %+v", hist)
	}
}

func TestShadowRestoreDeletesCreatedFiles(t *testing.T) {
	ws, gitDir := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(ws, "keep.txt"), []byte("k\n"), 0o644)
	cp, err := OpenShadow(gitDir, ws)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := cp.Commit("step_1", "before create", []string{"keep.txt", "new.txt"})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(ws, "new.txt"), []byte("created by agent\n"), 0o644)
	if err := cp.Restore(sha, []string{"keep.txt", "new.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, "new.txt")); !os.IsNotExist(err) {
		t.Error("file created after checkpoint must be removed on restore")
	}
}

func TestShadowGC(t *testing.T) {
	ws, gitDir := t.TempDir(), t.TempDir()
	cp, err := OpenShadow(gitDir, ws)
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(ws, "f.txt")
	shas := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		os.WriteFile(f, []byte{byte('a' + i)}, 0o644)
		sha, err := cp.Commit("step", "c", []string{"f.txt"})
		if err != nil {
			t.Fatal(err)
		}
		shas = append(shas, sha)
	}
	if err := cp.GC(3); err != nil {
		t.Fatal(err)
	}
	// fresh handle: the in-memory object cache would otherwise serve
	// pruned objects (real GC runs at project close, then the repo closes)
	cp2, err := OpenShadow(gitDir, ws)
	if err != nil {
		t.Fatal(err)
	}
	// newest kept commit still restorable
	if err := cp2.Restore(shas[4], []string{"f.txt"}); err != nil {
		t.Errorf("newest commit lost: %v", err)
	}
	// pruned commit is gone
	if err := cp2.Restore(shas[0], []string{"f.txt"}); err == nil {
		t.Error("oldest commit should have been pruned")
	}
}
