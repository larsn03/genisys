package parser

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/larsn03/genisys/internal/model"
)

func TestFromNativeSynthesizesIDs(t *testing.T) {
	calls := FromNative([]model.ToolCallDelta{
		{Index: 0, Name: "list_dir", Arguments: `{"path":`},
		{Index: 0, Arguments: `"."}`},
		{Index: 1, ID: "call_x", Name: "read_file_lines", Arguments: `{}`},
	})
	if len(calls) != 2 {
		t.Fatalf("calls = %d", len(calls))
	}
	if calls[0].ID != "tc_1" || calls[0].Function.Name != "list_dir" || calls[0].Function.Arguments != `{"path":"."}` {
		t.Errorf("call0 = %+v", calls[0])
	}
	if calls[1].ID != "call_x" {
		t.Errorf("call1 id = %q", calls[1].ID)
	}
}

func TestParseTextSimple(t *testing.T) {
	text := `Sure, let me look.
<read_file_lines>{"path": "main.go", "start_line": 1, "lines_to_read": 500}</read_file_lines>
That file is next.`
	prose, calls, errs := ParseText(text)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %d: %+v", len(calls), calls)
	}
	if calls[0].Function.Name != "read_file_lines" {
		t.Errorf("name = %q", calls[0].Function.Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
		t.Fatalf("args not valid JSON: %v (%q)", err, calls[0].Function.Arguments)
	}
	if args["path"] != "main.go" {
		t.Errorf("args = %v", args)
	}
	if strings.Contains(prose, "read_file_lines") {
		t.Errorf("call syntax leaked into prose: %q", prose)
	}
	if !strings.Contains(prose, "Sure, let me look.") || !strings.Contains(prose, "That file is next.") {
		t.Errorf("prose lost content: %q", prose)
	}
}

func TestParseTextMultipleCalls(t *testing.T) {
	text := `<list_dir>{"path": "a"}</list_dir> mid <list_dir>{"path": "b"}</list_dir>`
	_, calls, errs := ParseText(text)
	if len(errs) != 0 || len(calls) != 2 {
		t.Fatalf("calls=%d errs=%v", len(calls), errs)
	}
	if calls[0].ID == calls[1].ID {
		t.Error("ids must be unique")
	}
}

func TestParseTextTolerances(t *testing.T) {
	cases := []struct {
		name, text, wantName, wantArgs string
	}{
		{"fenced", "<list_dir>\n```json\n{\"path\": \".\"}\n```\n</list_dir>", "list_dir", `{"path": "."}`},
		{"single quotes", "<list_dir>{'path': '.'}</list_dir>", "list_dir", `{"path": "."}`},
		{"trailing comma", `<edit_file_tool>{"path": "x", "edits": [{"a": 1,},],}</edit_file_tool>`, "edit_file_tool", `{"path": "x", "edits": [{"a": 1}]}`},
		{"missing close", "<list_dir>{\"path\": \".\"}", "list_dir", `{"path": "."}`},
		{"empty body", "<get_scratchpad_folder></get_scratchpad_folder>", "get_scratchpad_folder", "{}"},
		{"python literals", `<web_search>{"objective": True, "max": None}</web_search>`, "web_search", `{"objective": true, "max": null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, calls, _ := ParseText(tc.text)
			if len(calls) != 1 {
				t.Fatalf("calls = %d", len(calls))
			}
			if calls[0].Function.Name != tc.wantName {
				t.Errorf("name = %q, want %q", calls[0].Function.Name, tc.wantName)
			}
			if !json.Valid([]byte(calls[0].Function.Arguments)) {
				t.Fatalf("args invalid: %q", calls[0].Function.Arguments)
			}
			var got, want any
			json.Unmarshal([]byte(calls[0].Function.Arguments), &got)
			json.Unmarshal([]byte(tc.wantArgs), &want)
			if !jsonEqual(got, want) {
				t.Errorf("args = %s, want %s", calls[0].Function.Arguments, tc.wantArgs)
			}
		})
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestParseTextAntml(t *testing.T) {
	text := `Let me read.
<antml:invoke name="read_file_lines">
<antml:parameter name="path">src/main.go</antml:parameter>
<antml:parameter name="start_line">1</antml:parameter>
</antml:invoke>`
	prose, calls, errs := ParseText(text)
	if len(calls) != 1 {
		t.Fatalf("calls = %d (errs %v, prose %q)", len(calls), errs, prose)
	}
	if calls[0].Function.Name != "read_file_lines" {
		t.Errorf("name = %q", calls[0].Function.Name)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
		t.Fatalf("args: %q: %v", calls[0].Function.Arguments, err)
	}
	if args["path"] != "src/main.go" || args["start_line"] != "1" {
		t.Errorf("args = %v", args)
	}
}

func TestParseTextIgnoresMarkupAndShortTags(t *testing.T) {
	text := "Use <b>bold</b> and <div>text</div> in HTML; <p> stays too."
	prose, calls, _ := ParseText(text)
	if len(calls) != 0 {
		t.Fatalf("false-positive calls: %+v", calls)
	}
	if !strings.Contains(prose, "<b>bold</b>") || !strings.Contains(prose, "<div>text</div>") {
		t.Errorf("markup lost: %q", prose)
	}
}

func TestParseTextUnrepairableKeepsRawAndReports(t *testing.T) {
	_, calls, errs := ParseText(`<list_dir>{"path": ???unclosed</list_dir>`)
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	if len(errs) == 0 {
		t.Error("expected repair error")
	}
	if !strings.Contains(calls[0].Function.Arguments, "???") {
		t.Errorf("raw args should survive: %q", calls[0].Function.Arguments)
	}
}

func TestRepairJSONDirect(t *testing.T) {
	ok := map[string]string{
		`{"a":1}`:                     `{"a":1}`,
		"{a:1}":                       `{"a":1}`,
		"{'a': 1}":                    `{"a":1}`,
		`{"a": [1, 2, ],}`:            `{"a": [1, 2]}`,
		`{"a": {"b": [1`:              `{"a": {"b": [1]}}`,
		"```json\n{\"a\": true}\n```": `{"a": true}`,
	}
	for in, want := range ok {
		got, err := RepairJSON(in)
		if err != nil {
			t.Errorf("RepairJSON(%q): %v", in, err)
			continue
		}
		var g, w any
		json.Unmarshal(got, &g)
		json.Unmarshal([]byte(want), &w)
		if !jsonEqual(g, w) {
			t.Errorf("RepairJSON(%q) = %s, want %s", in, got, want)
		}
	}
	if _, err := RepairJSON("{ \"a\": @@@@ ]]] "); err == nil {
		t.Error("expected failure on garbage")
	}
}
