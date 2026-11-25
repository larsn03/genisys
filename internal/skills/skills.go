// Package skills implements the Agent Skills format (SPEC §14):
// folders with SKILL.md (YAML frontmatter: name, description, optional
// allowed-tools), discovered from configured roots and rendered into the
// <available-skills> prompt block (progressive disclosure — the model
// reads SKILL.md itself when it decides to apply one).
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Skill is one discovered skill.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"` // absolute SKILL.md
	Root        string `json:"root"` // discovery root ("" = built-in)
	Builtin     bool   `json:"builtin"`
}

// name must be a safe folder-ish slug (agentskills.io convention).
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Parse reads SKILL.md content and extracts frontmatter fields.
func Parse(content string) (name, description string, err error) {
	if !strings.HasPrefix(content, "---") {
		return "", "", fmt.Errorf("skills: missing frontmatter")
	}
	rest := content[3:]
	if strings.HasPrefix(rest, "\r\n") {
		rest = rest[2:]
	} else if strings.HasPrefix(rest, "\n") {
		rest = rest[1:]
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", fmt.Errorf("skills: unterminated frontmatter")
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		switch strings.TrimSpace(k) {
		case "name":
			name = v
		case "description":
			description = v
		}
	}
	if name == "" || !nameRe.MatchString(name) {
		return "", "", fmt.Errorf("skills: invalid or missing name %q", name)
	}
	if description == "" {
		return "", "", fmt.Errorf("skills: %s: missing description", name)
	}
	return name, description, nil
}

// Discover walks the given roots one level (<root>/<dir>/SKILL.md) and
// returns skills sorted by name; later roots do not override earlier ones —
// first name wins (built-ins are passed first). Broken skills are skipped.
func Discover(roots ...string) []Skill {
	seen := map[string]bool{}
	var out []Skill
	for _, root := range roots {
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || seen[e.Name()] {
				continue
			}
			md := filepath.Join(root, e.Name(), "SKILL.md")
			b, err := os.ReadFile(md)
			if err != nil {
				continue
			}
			name, desc, err := Parse(string(b))
			if err != nil || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, Skill{Name: name, Description: desc, Path: md, Root: root})
		}
	}
	sortSkills(out)
	return out
}

func sortSkills(s []Skill) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].Name < s[j-1].Name; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// Install validates skill content and writes it under root as
// <name>/SKILL.md. Returns the written skill.
func Install(root, content string) (Skill, error) {
	name, desc, err := Parse(content)
	if err != nil {
		return Skill{}, err
	}
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Skill{}, err
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return Skill{}, err
	}
	return Skill{Name: name, Description: desc, Path: path, Root: root}, nil
}

// Remove deletes a discovered skill's folder (built-ins refuse).
func Remove(s Skill) error {
	if s.Builtin {
		return fmt.Errorf("skills: %s is built-in and cannot be removed", s.Name)
	}
	return os.RemoveAll(filepath.Dir(s.Path))
}

// Instruction is the harness line appended to the user message when the
// @-palette explicitly requests a skill (SPEC §14).
func Instruction(s Skill) string {
	return fmt.Sprintf("[user explicitly requested skill %q — read %s with read_file_lines first and follow it]", s.Name, s.Path)
}
