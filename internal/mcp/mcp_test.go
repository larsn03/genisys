package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/larsn03/genisys/internal/mcp"
	"github.com/larsn03/genisys/internal/model"
	"github.com/larsn03/genisys/internal/tools"
	"github.com/larsn03/genisys/internal/tools/mcptools"
)

// --- http transport ---------------------------------------------------------

func fakeMCPServer(t *testing.T) *httptest.Server {
	handshake := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&m)
		if m.ID == 0 { // notification
			w.WriteHeader(202)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch m.Method {
		case "initialize":
			handshake++
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"fake","version":"1"}}}`, m.ID)
		case "tools/list":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"echo","description":"echoes","inputSchema":{"type":"object","properties":{"v":{"type":"string"}}},"annotations":{"readOnlyHint":true}}]}}`, m.ID)
		case "tools/call":
			var p struct {
				Arguments struct {
					V string `json:"v"`
				}
			}
			_ = json.Unmarshal(m.Params, &p)
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"echo:%s"}]}}`, m.ID, p.Arguments.V)
		default:
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{}}`, m.ID)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPManager(t *testing.T) {
	srv := fakeMCPServer(t)
	mgr := mcp.NewManager([]mcp.ServerConfig{{Name: "fake", URL: srv.URL, Transport: "http"}})
	ctx := context.Background()

	// lazy start happens here (SPEC §15: first get_mcp_info)
	info := mgr.Info(ctx)
	if len(info) != 1 || !info[0].Online || len(info[0].Tools) != 1 || info[0].Tools[0] != "echo" {
		t.Fatalf("info=%+v", info)
	}
	defs, err := mgr.ToolSchemas(ctx, "fake")
	if err != nil || len(defs) != 1 || defs[0].Name != "echo" {
		t.Fatalf("defs=%+v err=%v", defs, err)
	}
	if !mgr.ReadOnly(ctx, "fake", "echo") {
		t.Error("readOnlyHint not honored")
	}
	res, err := mgr.Call(ctx, "fake", "echo", json.RawMessage(`{"v":"hi"}`))
	if err != nil || res.Text != "echo:hi" || res.IsError {
		t.Fatalf("call=%+v err=%v", res, err)
	}
	mgr.CloseAll()
}

// --- stdio transport --------------------------------------------------------

func TestStdioManager(t *testing.T) {
	t.Parallel()
	bin := filepath.Join(t.TempDir(), "echo-mcp")
	out, err := exec.Command("go", "build", "-o", bin, "./testserver").CombinedOutput()
	if err != nil {
		t.Skipf("go build testserver: %v: %s", err, out)
	}
	mgr := mcp.NewManager([]mcp.ServerConfig{{Name: "echo", Command: bin, Transport: "stdio"}})
	ctx := context.Background()
	info := mgr.Info(ctx)
	if len(info) != 1 || !info[0].Online {
		t.Fatalf("info=%+v", info)
	}
	if !mgr.ReadOnly(ctx, "echo", "read_thing") || mgr.ReadOnly(ctx, "echo", "write_thing") {
		t.Error("annotations wrong")
	}
	res, err := mgr.Call(ctx, "echo", "read_thing", json.RawMessage(`{"v":"42"}`))
	if err != nil || res.Text != "read_thing:42" {
		t.Fatalf("call=%+v err=%v", res, err)
	}
	// idle recycle: closing then calling must transparently restart
	mgr.CloseAll()
	if _, err := mgr.Call(ctx, "echo", "write_thing", json.RawMessage(`{"v":"7"}`)); err != nil {
		t.Fatalf("restart: %v", err)
	}
	mgr.CloseAll()
	_ = os.Remove(bin)
}

// --- disclosure tools --------------------------------------------------------

func TestDisclosureTools(t *testing.T) {
	srv := fakeMCPServer(t)
	mgr := mcp.NewManager([]mcp.ServerConfig{{Name: "fake", URL: srv.URL, Transport: "http"}})
	defer mgr.CloseAll()

	// P5-6 DoD: MCP schemas never appear in the base tool definitions —
	// only the three disclosure tools exist.
	reg := tools.NewRegistry()
	for _, tl := range mcptools.Tools() {
		reg.Add(tl)
	}
	defs, _ := json.Marshal(reg.Definitions())
	s := string(defs)
	if strings.Contains(s, "echo") {
		t.Errorf("MCP tool leaked into base schema: %s", s)
	}
	for _, want := range []string{"get_mcp_info", "get_mcp_tools_info", "call_mcp_tool"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing disclosure tool %s", want)
		}
	}

	w := &tools.World{MCP: mgr}
	call := func(name, args string) tools.Result {
		return reg.Execute(context.Background(), model.ToolCall{ID: "c", Type: "function",
			Function: model.ToolCallFunction{Name: name, Arguments: args}}, w)
	}
	if r := call("get_mcp_info", `{}`); !strings.Contains(r.Content, "fake: online — tools: echo") {
		t.Errorf("info: %q", r.Content)
	}
	if r := call("get_mcp_tools_info", `{"server":"fake"}`); !strings.Contains(r.Content, `"echo"`) {
		t.Errorf("tools_info: %q", r.Content)
	}
	// readOnlyHint tool runs without a broker
	if r := call("call_mcp_tool", `{"server":"fake","tool":"echo","arguments":{"v":"x"}}`); r.IsError || r.Content != "echo:x" {
		t.Errorf("call: %+v", r)
	}
}
