package skills_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/larsn03/genisys/internal/skills"
)

const claudeSkill = `---
name: pdf-forms
description: Fill PDF forms programmatically. Use when the user mentions PDF forms or acroforms.
allowed-tools: Bash(python*) Read
---

# PDF forms
Step 1: read the form fields.
`

func TestParse(t *testing.T) {
	n, d, err := skills.Parse(claudeSkill)
	if err != nil || n != "pdf-forms" || !strings.HasPrefix(d, "Fill PDF forms") {
		t.Fatalf("got %q %q %v", n, d, err)
	}
	if _, _, err := skills.Parse("no frontmatter"); err == nil {
		t.Error("missing frontmatter must fail")
	}
	if _, _, err := skills.Parse("---\nname: Bad Name\n---\n"); err == nil {
		t.Error("uppercase name must fail")
	}
}

func TestDiscoverClaudeCodeCompat(t *testing.T) {
	// P5-4 DoD: a Claude-Code-style skill folder runs unmodified.
	root := t.TempDir()
	dir := filepath.Join(root, "pdf-forms")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(claudeSkill), 0o644)
	os.WriteFile(filepath.Join(dir, "helper.py"), []byte("print(1)"), 0o644)
	// broken sibling is skipped
	os.MkdirAll(filepath.Join(root, "broken"), 0o755)
	os.WriteFile(filepath.Join(root, "broken", "SKILL.md"), []byte("junk"), 0o644)

	got := skills.Discover(root)
	if len(got) != 1 {
		t.Fatalf("skills=%d want 1", len(got))
	}
	s := got[0]
	if s.Name != "pdf-forms" || !strings.HasSuffix(s.Path, "SKILL.md") {
		t.Errorf("skill=%+v", s)
	}
}

func TestDiscoverFirstRootWins(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, tc := range []struct {
		root, desc string
	}{
		{a, "first wins"},
		{b, "should be shadowed"},
	} {
		d := filepath.Join(tc.root, "dup")
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "SKILL.md"),
			[]byte("---\nname: dup\ndescription: "+tc.desc+"\n---\n"), 0o644)
	}
	got := skills.Discover(a, b)
	if len(got) != 1 || got[0].Description != "first wins" {
		t.Fatalf("got %+v", got)
	}
}

func TestInstallAndRemove(t *testing.T) {
	root := t.TempDir()
	sk, err := skills.Install(root, claudeSkill)
	if err != nil {
		t.Fatal(err)
	}
	if sk.Name != "pdf-forms" || !strings.HasSuffix(sk.Path, "pdf-forms/SKILL.md") {
		t.Errorf("installed=%+v", sk)
	}
	if _, err := os.Stat(sk.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := skills.Install(root, "no frontmatter"); err == nil {
		t.Error("invalid content must not install")
	}
	if err := skills.Remove(sk); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(sk.Path)); !os.IsNotExist(err) {
		t.Error("remove must delete the folder")
	}
}

func TestBuiltinMaterialize(t *testing.T) {
	root := t.TempDir()
	b := skills.MaterializeBuiltin(root)
	names := map[string]bool{}
	for _, s := range b {
		names[s.Name] = true
		if !s.Builtin {
			t.Errorf("%s not flagged builtin", s.Name)
		}
		if _, err := os.ReadFile(s.Path); err != nil {
			t.Errorf("%s not readable on disk: %v", s.Name, err)
		}
	}
	for _, want := range []string{"skill-management", "documents-presentations-spreadsheets"} {
		if !names[want] {
			t.Errorf("missing builtin %s (have %v)", want, names)
		}
	}
	if err := skills.Remove(b[0]); err == nil {
		t.Error("builtins must refuse removal")
	}
}
