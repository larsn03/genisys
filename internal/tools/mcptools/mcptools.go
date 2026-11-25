// Package mcptools exposes MCP servers through the progressive
// disclosure trio (SPEC §15/§9.8): get_mcp_info → get_mcp_tools_info →
// call_mcp_tool. MCP tool schemas never enter the base system prompt.
package mcptools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/larsn03/genisys/internal/tools"
	"github.com/larsn03/genisys/internal/workspace"
)

// Tools returns the three disclosure tools.
func Tools() []tools.Tool {
	return []tools.Tool{info{}, toolsInfo{}, call{}}
}

type info struct{}

func (info) Name() string { return "get_mcp_info" }
func (info) Description() string {
	return "List configured MCP servers with their tool NAMES (starts idle servers). Call this before get_mcp_tools_info/call_mcp_tool."
}
func (info) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

func (info) Execute(ctx context.Context, _ json.RawMessage, w *tools.World) tools.Result {
	if w.MCP == nil || w.MCP.Empty() {
		return tools.Result{Content: "no MCP servers configured.", IsError: true}
	}
	var b strings.Builder
	for _, si := range w.MCP.Info(ctx) {
		if si.Online {
			fmt.Fprintf(&b, "%s: online — tools: %s\n", si.Name, strings.Join(si.Tools, ", "))
		} else {
			fmt.Fprintf(&b, "%s: OFFLINE (%s)\n", si.Name, si.Error)
		}
	}
	return tools.Result{Content: b.String()}
}

type toolsInfo struct{}

func (toolsInfo) Name() string { return "get_mcp_tools_info" }
func (toolsInfo) Description() string {
	return "Get full tool definitions (schemas) for one MCP server, for use with call_mcp_tool."
}
func (toolsInfo) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"server":{"type":"string"}},"required":["server"],"additionalProperties":false}`)
}

func (toolsInfo) Execute(ctx context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Server string `json:"server"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil || a.Server == "" {
		return tools.Result{Content: "invalid_args: server is required", IsError: true}
	}
	if w.MCP == nil {
		return tools.Result{Content: "error: no MCP servers configured", IsError: true}
	}
	defs, err := w.MCP.ToolSchemas(ctx, a.Server)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	b, _ := json.MarshalIndent(defs, "", "  ")
	return tools.Result{Content: string(b)}
}

type call struct{}

func (call) Name() string { return "call_mcp_tool" }
func (call) Description() string {
	return "Execute one MCP tool. Get the schema from get_mcp_tools_info first."
}
func (call) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"server":{"type":"string"},"tool":{"type":"string"},
"arguments":{"type":"object","description":"Tool arguments per its schema."}},
"required":["server","tool"],"additionalProperties":false}`)
}

func (call) Execute(ctx context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Server    string          `json:"server"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil || a.Server == "" || a.Tool == "" {
		return tools.Result{Content: "invalid_args: server and tool are required", IsError: true}
	}
	if w.MCP == nil {
		return tools.Result{Content: "error: no MCP servers configured", IsError: true}
	}
	// SPEC §15: MCP results are permission-gated like any tool. Tools
	// annotated readOnlyHint run freely; anything else needs a session
	// grant (pseudo-path keeps the UI card readable).
	if !w.MCP.ReadOnly(ctx, a.Server, a.Tool) {
		if err := w.Perms.Request(ctx, "mcp/"+a.Server+"/"+a.Tool, workspace.ModeRW,
			"MCP tool may read or write outside granted paths"); err != nil {
			return tools.Result{Content: "error: " + err.Error(), IsError: true}
		}
	}
	res, err := w.MCP.Call(ctx, a.Server, a.Tool, a.Arguments)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	if res.IsError {
		return tools.Result{Content: "mcp tool error:\n" + res.Text, IsError: true}
	}
	if res.Text == "" {
		res.Text = "(empty result)"
	}
	return tools.Result{Content: res.Text}
}
