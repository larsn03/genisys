// echo MCP server for stdio integration tests: newline-delimited
// JSON-RPC on stdin/stdout. tools: read_thing (readOnlyHint) +
// write_thing (no hint).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type msg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

func main() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var m msg
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.ID == 0 {
			continue
		}
		var res any
		switch m.Method {
		case "initialize":
			res = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{},
				"serverInfo": map[string]any{"name": "echo", "version": "1"}}
		case "tools/list":
			res = map[string]any{"tools": []any{
				map[string]any{"name": "read_thing", "description": "read it",
					"inputSchema": map[string]any{"type": "object"},
					"annotations": map[string]any{"readOnlyHint": true}},
				map[string]any{"name": "write_thing", "description": "write it",
					"inputSchema": map[string]any{"type": "object"}},
			}}
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(m.Params, &p)
			res = map[string]any{"content": []any{
				map[string]any{"type": "text", "text": fmt.Sprintf("%s:%v", p.Name, p.Arguments["v"])}}}
		default:
			res = map[string]any{}
		}
		b, _ := json.Marshal(msg{JSONRPC: "2.0", ID: m.ID, Result: mustJSON(res)})
		fmt.Println(string(b))
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
