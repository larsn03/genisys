// Package fileio implements read_file_lines, read_file_chars and list_dir
// (SPEC §9.1). P2 wraps these in the grant-checked WorkspaceWriter path; P1
// resolves relative paths from the session working directory.
package fileio

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/larsn03/genisys/internal/tools"
)

// --- read_file_lines --------------------------------------------------------

type ReadLines struct{}

func (ReadLines) Name() string { return "read_file_lines" }
func (ReadLines) Description() string {
	return "Read UTF-8 text lines. The result always ends with <EOF> or a continuation hint. Long lines may include a read_file_chars hint."
}
func (ReadLines) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"path":{"type":"string","description":"Path to the text file. Relative paths resolve from the working directory."},
"start_line":{"type":["integer","null"],"description":"1-indexed line to start from. Default: 1."},
"lines_to_read":{"type":["integer","null"],"description":"Max lines. Default: 500."}},
"required":["path"],"additionalProperties":false}`)
}

func (ReadLines) Execute(_ context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Path        string `json:"path"`
		StartLine   *int   `json:"start_line"`
		LinesToRead *int   `json:"lines_to_read"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil {
		return tools.Result{Content: "invalid_json: " + err.Error(), IsError: true}
	}
	if a.Path == "" {
		return tools.Result{Content: "invalid_args: path is required", IsError: true}
	}
	start, count := 1, 500
	if a.StartLine != nil && *a.StartLine > 0 {
		start = *a.StartLine
	}
	if a.LinesToRead != nil && *a.LinesToRead > 0 {
		count = *a.LinesToRead
	}
	p, err := w.Access(a.Path, false)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	if !utf8.Valid(data) {
		return tools.Result{Content: "error: file is not valid UTF-8 text", IsError: true}
	}
	w.MarkRead(p)
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if start > len(lines) {
		return tools.Result{Content: fmt.Sprintf("<EOF> (file has %d lines)", len(lines))}
	}
	end := min(start-1+count, len(lines))
	var b strings.Builder
	longLine := false
	for i := start - 1; i < end; i++ {
		fmt.Fprintf(&b, "%d\t%s\n", i+1, lines[i])
		if len(lines[i]) > 1000 {
			longLine = true
		}
	}
	if end >= len(lines) {
		b.WriteString("<EOF>")
	} else {
		b.WriteString(fmt.Sprintf("[continuation: %d more lines — call again with start_line=%d]", len(lines)-end, end+1))
	}
	if longLine {
		b.WriteString("\n[hint: contains long lines — use read_file_chars for exact slices]")
	}
	return tools.Result{Content: b.String()}
}

// --- read_file_chars --------------------------------------------------------

type ReadChars struct{}

func (ReadChars) Name() string { return "read_file_chars" }
func (ReadChars) Description() string {
	return "Read UTF-8 text by character offset from a file."
}
func (ReadChars) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"path":{"type":"string","description":"Path to the text file. Relative paths resolve from the working directory."},
"start_char":{"type":["integer","null"],"description":"0-indexed character offset. Default: 0."},
"chars_to_read":{"type":["integer","null"],"description":"Max characters. Default: 2500."}},
"required":["path"],"additionalProperties":false}`)
}

func (ReadChars) Execute(_ context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Path        string `json:"path"`
		StartChar   *int   `json:"start_char"`
		CharsToRead *int   `json:"chars_to_read"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil {
		return tools.Result{Content: "invalid_json: " + err.Error(), IsError: true}
	}
	if a.Path == "" {
		return tools.Result{Content: "invalid_args: path is required", IsError: true}
	}
	start, count := 0, 2500
	if a.StartChar != nil && *a.StartChar > 0 {
		start = *a.StartChar
	}
	if a.CharsToRead != nil && *a.CharsToRead > 0 {
		count = *a.CharsToRead
	}
	p, err := w.Access(a.Path, false)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	w.MarkRead(p)
	r := []rune(string(data))
	if start >= len(r) {
		return tools.Result{Content: "<EOF> (file has " + fmt.Sprint(len(r)) + " characters)"}
	}
	end := min(start+count, len(r))
	out := string(r[start:end])
	if end >= len(r) {
		out += "<EOF>"
	} else {
		out += fmt.Sprintf("[continuation: %d more characters — call again with start_char=%d]", len(r)-end, end)
	}
	return tools.Result{Content: out}
}

// --- list_dir ---------------------------------------------------------------

type ListDir struct{}

func (ListDir) Name() string { return "list_dir" }
func (ListDir) Description() string {
	return "List a directory. Directories come first (prefixed with D), then files (prefixed with F); both are alphabetical."
}
func (ListDir) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"path":{"type":"string","description":"Directory path. Relative paths resolve from the working directory; empty means the working directory."}},
"required":["path"],"additionalProperties":false}`)
}

func (ListDir) Execute(_ context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Path string `json:"path"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil {
		return tools.Result{Content: "invalid_json: " + err.Error(), IsError: true}
	}
	dir := w.Cwd
	if a.Path != "" {
		d, err := w.Access(a.Path, false)
		if err != nil {
			return tools.Result{Content: "error: " + err.Error(), IsError: true}
		}
		dir = d
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	var dirs, files []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, "D "+e.Name())
		} else {
			files = append(files, "F "+e.Name())
		}
	}
	sortStrings(dirs)
	sortStrings(files)
	out := append(dirs, files...)
	if len(out) == 0 {
		out = []string{"(empty)"}
	}
	return tools.Result{Content: strings.Join(out, "\n")}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
