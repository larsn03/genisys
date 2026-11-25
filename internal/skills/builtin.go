// builtin.go — skills shipped in the binary (SPEC §14 skill-management
// built-in + the documents skill). They are materialized to disk on
// startup so the model can read them with read_file_lines like any other
// skill; the binary version always wins (upgrade-safe).

package skills

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:builtin
var builtinFS embed.FS

// MaterializeBuiltin writes the embedded skills under root/<name>/SKILL.md
// and returns them as discovered skills flagged builtin.
func MaterializeBuiltin(root string) []Skill {
	var out []Skill
	_ = fs.WalkDir(builtinFS, "builtin", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(p) != "SKILL.md" {
			return nil
		}
		name := filepath.Base(filepath.Dir(p))
		b, err := builtinFS.ReadFile(p)
		if err != nil {
			return nil
		}
		sname, desc, err := Parse(string(b))
		if err != nil || sname != name {
			return nil
		}
		dir := filepath.Join(root, name)
		path := filepath.Join(dir, "SKILL.md")
		// overwrite so binary upgrades propagate
		if os.MkdirAll(dir, 0o755) == nil && os.WriteFile(path, b, 0o644) == nil {
			out = append(out, Skill{Name: sname, Description: desc, Path: path, Root: root, Builtin: true})
		}
		return nil
	})
	sortSkills(out)
	return out
}
