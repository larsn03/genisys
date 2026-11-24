package parser

import (
	"testing"
)

// FuzzParse is the IMPL §13 fuzz target: no input may panic the parser, and
// any call it emits must carry string arguments.
func FuzzParse(f *testing.F) {
	seeds := []string{
		`<list_dir>{"path": "."}</list_dir>`,
		"```json\n{\"a\": 1}\n```",
		"<a>{</a>",
		"<tool>{'k': 'v',}</tool",
		"<antml:invoke name=\"x\"><antml:parameter name=\"p\">1</antml:parameter></antml:invoke>",
		"<b><c></c></b>",
		"{\"unbalanced\": [1, 2",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		_, calls, _ := ParseText(in)
		for _, c := range calls {
			_ = c.Function.Name
			_ = c.Function.Arguments
		}
		if _, err := RepairJSON(in); err != nil {
			_ = err // errors fine, panics not
		}
	})
}
