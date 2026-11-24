package tools_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/larsn03/genisys/internal/model"
	"github.com/larsn03/genisys/internal/tools"
	exectool "github.com/larsn03/genisys/internal/tools/exec"
	"github.com/larsn03/genisys/internal/tools/fileio"
	"github.com/larsn03/genisys/internal/tools/search"
)

func fixtureWorld(t *testing.T) *tools.World {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return &tools.World{
		Cwd:          filepath.Join(filepath.Dir(thisFile), "..", "..", "testdata", "fixturerepo"),
		Scratchpad:   t.TempDir(),
		ShellTimeout: 10000,
	}
}

func reg() *tools.Registry {
	r := tools.NewRegistry()
	r.Add(fileio.ReadLines{})
	r.Add(fileio.ReadChars{})
	r.Add(fileio.ListDir{})
	r.Add(search.SearchLine{})
	r.Add(search.FindFiles{})
	r.Add(exectool.Shell{})
	return r
}

func call(t *testing.T, r *tools.Registry, name, args string, w *tools.World) tools.Result {
	t.Helper()
	return r.Execute(context.Background(), model.ToolCall{
		ID: "tc_1", Type: "function",
		Function: model.ToolCallFunction{Name: name, Arguments: args},
	}, w)
}

func TestRegistryBasics(t *testing.T) {
	r := reg()
	names := r.Names()
	if len(names) != 6 {
		t.Fatalf("tools = %v", names)
	}
	defs := r.Definitions()
	for _, d := range defs {
		if d.Function.Name == "" || d.Function.Description == "" || !json.Valid(d.Function.Parameters) {
			t.Errorf("bad definition: %+v", d)
		}
	}
	// unknown tool → error listing valid tools (SPEC §23)
	res := call(t, r, "no_such_tool", "{}", fixtureWorld(t))
	if !res.IsError || !strings.Contains(res.Content, "Valid tools:") {
		t.Errorf("unknown tool result: %+v", res)
	}
	// invalid JSON args
	res = call(t, r, "list_dir", "{oops", fixtureWorld(t))
	if !res.IsError || !strings.HasPrefix(res.Content, "invalid_json") {
		t.Errorf("bad json result: %+v", res)
	}
}

func TestReadFileLines(t *testing.T) {
	w := fixtureWorld(t)
	res := call(t, reg(), "read_file_lines", `{"path":"data.txt"}`, w)
	if res.IsError {
		t.Fatal(res.Content)
	}
	if !strings.Contains(res.Content, "1\tline one") || !strings.HasSuffix(res.Content, "<EOF>") {
		t.Errorf("content: %q", res.Content)
	}
	res = call(t, reg(), "read_file_lines", `{"path":"data.txt","start_line":2,"lines_to_read":1}`, w)
	if strings.Contains(res.Content, "line one") || !strings.Contains(res.Content, "continuation") {
		t.Errorf("windowed read: %q", res.Content)
	}
}

func TestReadFileChars(t *testing.T) {
	res := call(t, reg(), "read_file_chars", `{"path":"data.txt","chars_to_read":9}`, fixtureWorld(t))
	if !strings.HasPrefix(res.Content, "line one\n") || !strings.Contains(res.Content, "continuation") {
		t.Errorf("content: %q", res.Content)
	}
}

func TestListDir(t *testing.T) {
	res := call(t, reg(), "list_dir", `{"path":""}`, fixtureWorld(t))
	if res.IsError {
		t.Fatal(res.Content)
	}
	// dirs first (D), files after (F), alphabetical
	want := []string{"D ignored", "D src", "F .gitignore", "F data.txt", "F main.go"}
	got := strings.Split(res.Content, "\n")
	for i, wt := range want {
		if i >= len(got) || got[i] != wt {
			t.Fatalf("listing = %q", res.Content)
		}
	}
}

func TestSearchFileLine(t *testing.T) {
	r := reg()
	w := fixtureWorld(t)
	// lowercase pattern → case-insensitive; finds main.go
	res := call(t, r, "search_file_line", `{"pattern":"requesttimeout"}`, w)
	if res.IsError || !strings.Contains(res.Content, "main.go:4:") {
		t.Errorf("search: %q", res.Content)
	}
	// ignore rules: ignored/skipme.txt must not match
	if strings.Contains(res.Content, "skipme") {
		t.Errorf("ignored file searched: %q", res.Content)
	}
	res = call(t, r, "search_file_line", `{"pattern":"timeout","include_ignored":true}`, w)
	if !strings.Contains(res.Content, "skipme") {
		t.Errorf("include_ignored: %q", res.Content)
	}
	// no matches
	res = call(t, r, "search_file_line", `{"pattern":"zzz_nothing_zzz"}`, w)
	if res.Content != "no matches" {
		t.Errorf("no matches: %q", res.Content)
	}
}

func TestFindFiles(t *testing.T) {
	res := call(t, reg(), "find_files", `{"glob":"**/*.go"}`, fixtureWorld(t))
	if res.IsError || !strings.Contains(res.Content, "main.go") || !strings.Contains(res.Content, "src/handler.go") {
		t.Errorf("glob: %q", res.Content)
	}
	res = call(t, reg(), "find_files", `{"glob":"[bad"}`, fixtureWorld(t))
	if !res.IsError {
		t.Error("bad glob should error")
	}
}

func TestShellCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-flavored assertions")
	}
	r, w := reg(), fixtureWorld(t)

	res := call(t, r, "shell_command", `{"command":"echo hello && pwd"}`, w)
	if res.IsError || !strings.Contains(res.Content, "exit code: 0") || !strings.Contains(res.Content, "hello") {
		t.Errorf("echo: %q", res.Content)
	}
	if !strings.Contains(res.Content, "summarize anything relevant") {
		t.Error("missing §7.3 note")
	}
	// non-zero exit
	res = call(t, r, "shell_command", `{"command":"exit 3"}`, w)
	if !res.IsError || !strings.Contains(res.Content, "exit code: 3") {
		t.Errorf("exit: %q", res.Content)
	}
	// timeout kills the process group
	start := time.Now()
	res = call(t, r, "shell_command", `{"command":"echo partial; sleep 30","timeout_ms":300}`, w)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("timeout not enforced (%v)", elapsed)
	}
	if !res.IsError || !strings.Contains(res.Content, "timeout after 300ms") || !strings.Contains(res.Content, "partial") {
		t.Errorf("timeout: %q", res.Content)
	}
	// workdir resolution
	res = call(t, r, "shell_command", `{"command":"pwd","workdir":"src"}`, w)
	if !strings.Contains(res.Content, "/src\n") {
		t.Errorf("workdir: %q", res.Content)
	}
}

func TestShapeTruncation(t *testing.T) {
	long := strings.Repeat("line of text\n", 2000)
	got := tools.Shape(long, tools.MaxBytes, tools.MaxLines)
	if len(got) > tools.MaxBytes+len("\n[…truncated — re-run with narrower arguments]") {
		t.Errorf("not truncated: %d bytes", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Error("missing marker")
	}
}
