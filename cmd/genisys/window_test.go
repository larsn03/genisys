package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/larsn03/genisys/internal/app"
)

func TestOpenArgs(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "proj")
	os.MkdirAll(sub, 0o755)
	f := filepath.Join(sub, "main.go")
	os.WriteFile(f, []byte("package main"), 0o644)

	cwd, file, err := openArgs(sub)
	if err != nil || cwd != sub || file != "" {
		t.Fatalf("dir: %q %q %v", cwd, file, err)
	}
	cwd, file, err = openArgs(f)
	if err != nil || cwd != sub || file != f {
		t.Fatalf("file: %q %q %v", cwd, file, err)
	}
	if _, _, err := openArgs(filepath.Join(dir, "nope")); err == nil {
		t.Fatal("missing path must error")
	}
}

func TestOpenURLParams(t *testing.T) {
	root := t.TempDir()
	a, err := app.Open(filepath.Join(root, "missing-config.yaml"), filepath.Join(root, "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	ws := filepath.Join(root, "workspace")
	os.MkdirAll(filepath.Join(ws, "src"), 0o755)
	os.WriteFile(filepath.Join(ws, "src", "app.ts"), []byte("x"), 0o644)

	// dir → project created on demand
	extra := openURLParams(a, ws, "")
	if !strings.HasPrefix(extra, "&project=") || len(extra) < 12 {
		t.Fatalf("extra=%q", extra)
	}
	// same dir again → same project (idempotent)
	if again := openURLParams(a, ws, ""); again != extra {
		t.Fatalf("not idempotent: %q vs %q", again, extra)
	}

	// file inside → &file=src/app.ts
	extra2 := openURLParams(a, ws, filepath.Join(ws, "src", "app.ts"))
	q, err := url.ParseQuery(strings.TrimPrefix(extra2, "&"))
	if err != nil {
		t.Fatal(err)
	}
	if q.Get("file") != "src/app.ts" {
		t.Fatalf("file=%q", q.Get("file"))
	}

	// file outside the workspace → no file param, project still set
	extra3 := openURLParams(a, ws, filepath.Join(root, "elsewhere.txt"))
	if !strings.HasPrefix(extra3, "&project=") || strings.Contains(extra3, "&file=") {
		t.Fatalf("outside: %q", extra3)
	}

	// file arg pointing straight at a file also resolves its dir project
	extra4 := openURLParams(a, filepath.Join(ws, "src", "app.ts"), "")
	if !strings.HasPrefix(extra4, "&project=") {
		t.Fatalf("file-as-target: %q", extra4)
	}
}
