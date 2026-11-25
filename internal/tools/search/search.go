// Package search implements search_file_line and find_files (SPEC §9.1):
// lexical repo search with ignore rules, ripgrep-style semantics, pure Go.
package search

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	ignore "github.com/sabhiram/go-gitignore"

	"github.com/larsn03/genisys/internal/tools"
)

const maxFileBytes = 10 << 20 // 10 MiB, SPEC §9.1

// --- shared walker with gitignore support -----------------------------------

type walker struct {
	root     string
	matchers []*ignore.GitIgnore // stack-like: appended when entering a dir
	noIgnore bool                // include_ignored: never consult rules
}

// walk calls fn for every non-ignored file; fn returns false to stop.
func (w *walker) walk(dir string, fn func(path, rel string) bool) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	if m := loadIgnore(filepath.Join(dir, ".gitignore")); m != nil && !w.noIgnore {
		w.matchers = append(w.matchers, m)
		defer func() { w.matchers = w.matchers[:len(w.matchers)-1] }()
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		name := e.Name()
		if name == ".git" || name == "node_modules" || (name != "." && strings.HasPrefix(name, ".")) {
			continue
		}
		path := filepath.Join(dir, name)
		rel, _ := filepath.Rel(w.root, path)
		if w.ignored(rel) {
			continue
		}
		if e.IsDir() {
			if !w.walk(path, fn) {
				return false
			}
			continue
		}
		if !fn(path, filepath.ToSlash(rel)) {
			return false
		}
	}
	return true
}

func (w *walker) ignored(rel string) bool {
	for _, m := range w.matchers {
		if m.MatchesPath(rel) {
			return true
		}
	}
	return false
}

func loadIgnore(path string) *ignore.GitIgnore {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	gi, err := ignore.CompileIgnoreFile(path)
	if err != nil {
		return nil
	}
	return gi
}

// --- search_file_line ---------------------------------------------------------

type SearchLine struct{}

func (SearchLine) Name() string { return "search_file_line" }
func (SearchLine) Description() string {
	return `Search UTF-8 text files with literal text or a regular expression. Patterns without uppercase letters are case-insensitive; patterns containing uppercase letters are case-sensitive. Files larger than 10 MiB are not searched. Returns matching line numbers with bounded context and respects repository ignore rules by default.`
}
func (SearchLine) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"pattern":{"type":"string","description":"Literal text or regex pattern"},
"path":{"type":["string","null"],"description":"File or directory to search. Default: working directory."},
"is_regex":{"type":"boolean","default":false,"description":"Treat pattern as a Rust-crate-style regex."},
"context_lines":{"type":"integer","maximum":10,"default":2,"description":"Context lines around each match."},
"include_ignored":{"type":"boolean","default":false,"description":"Also search ignored files."},
"max_results":{"type":"integer","default":200,"description":"Maximum matching lines to return."}},
"required":["pattern"],"additionalProperties":false}`)
}

func (SearchLine) Execute(ctx context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Pattern        string  `json:"pattern"`
		Path           *string `json:"path"`
		IsRegex        bool    `json:"is_regex"`
		ContextLines   *int    `json:"context_lines"`
		IncludeIgnored bool    `json:"include_ignored"`
		MaxResults     *int    `json:"max_results"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil {
		return tools.Result{Content: "invalid_json: " + err.Error(), IsError: true}
	}
	if a.Pattern == "" {
		return tools.Result{Content: "invalid_args: pattern is required", IsError: true}
	}
	expr := a.Pattern
	if !a.IsRegex {
		expr = regexp.QuoteMeta(expr)
	}
	if !hasUpper(a.Pattern) {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return tools.Result{Content: "invalid_args: bad regex: " + err.Error(), IsError: true}
	}
	ctxLines := 2
	if a.ContextLines != nil && *a.ContextLines >= 0 {
		ctxLines = *a.ContextLines
	}
	maxRes := 200
	if a.MaxResults != nil && *a.MaxResults > 0 {
		maxRes = *a.MaxResults
	}
	root := w.Cwd
	if a.Path != nil && *a.Path != "" {
		root = tools.Resolve(w.Cwd, *a.Path)
	}
	st := &walker{root: root, noIgnore: a.IncludeIgnored}
	var out []string
	found := 0
	stop := false

	searchOne := func(path, rel string) bool {
		if st.ignored(rel) {
			return true
		}
		info, err := os.Stat(path)
		if err != nil || info.Size() > maxFileBytes {
			return true
		}
		data, err := os.ReadFile(path)
		if err != nil || isBinary(data) {
			return true
		}
		lines := strings.Split(string(data), "\n")
		for i, ln := range lines {
			if err := ctx.Err(); err != nil {
				stop = true
				return false
			}
			if !re.MatchString(ln) {
				continue
			}
			lo, hi := max(i-ctxLines, 0), min(i+ctxLines+1, len(lines))
			for j := lo; j < hi; j++ {
				sep := "-"
				if j == i {
					sep = ":"
				}
				out = append(out, fmt.Sprintf("%s:%d%s%s", rel, j+1, sep, lines[j]))
			}
			out = append(out, "--")
			found++
			if found >= maxRes {
				return false
			}
		}
		return true
	}

	info, err := os.Stat(root)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	if info.IsDir() {
		st.walk(root, searchOne)
	} else {
		searchOne(root, filepath.Base(root)) // single-file search; stop signal irrelevant
	}
	_ = stop
	if found == 0 {
		return tools.Result{Content: "no matches"}
	}
	res := strings.Join(out, "\n")
	if found >= maxRes {
		res += fmt.Sprintf("\n[truncated at max_results=%d]", maxRes)
	}
	return tools.Result{Content: res}
}

// --- find_files ---------------------------------------------------------------

type FindFiles struct{}

func (FindFiles) Name() string { return "find_files" }
func (FindFiles) Description() string {
	return "Find files whose paths match a ripgrep-style glob. Respects repository ignore rules by default."
}
func (FindFiles) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"glob":{"type":"string","description":"e.g. **/*.tsx or src/**/*.{ts,js}"},
"path":{"type":["string","null"],"description":"Search root. Default: working directory."},
"max_results":{"type":"integer","default":500}},
"required":["glob"],"additionalProperties":false}`)
}

func (FindFiles) Execute(_ context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Glob       string  `json:"glob"`
		Path       *string `json:"path"`
		MaxResults *int    `json:"max_results"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil {
		return tools.Result{Content: "invalid_json: " + err.Error(), IsError: true}
	}
	if a.Glob == "" {
		return tools.Result{Content: "invalid_args: glob is required", IsError: true}
	}
	if !doublestar.ValidatePattern(a.Glob) {
		return tools.Result{Content: "invalid_args: bad glob pattern", IsError: true}
	}
	maxRes := 500
	if a.MaxResults != nil && *a.MaxResults > 0 {
		maxRes = *a.MaxResults
	}
	root := w.Cwd
	if a.Path != nil && *a.Path != "" {
		root = tools.Resolve(w.Cwd, *a.Path)
	}
	st := &walker{root: root}
	var out []string
	st.walk(root, func(path, rel string) bool {
		if st.ignored(rel) {
			return true
		}
		if ok, _ := doublestar.Match(a.Glob, rel); ok {
			out = append(out, rel)
			if len(out) >= maxRes {
				return false
			}
		}
		return true
	})
	if len(out) == 0 {
		return tools.Result{Content: "no matches"}
	}
	res := strings.Join(out, "\n")
	if len(out) >= maxRes {
		res += fmt.Sprintf("\n[truncated at max_results=%d]", maxRes)
	}
	return tools.Result{Content: res}
}

// --- helpers ------------------------------------------------------------------

func hasUpper(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			return true
		}
	}
	return false
}

func isBinary(data []byte) bool {
	n := min(len(data), 512)
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}
