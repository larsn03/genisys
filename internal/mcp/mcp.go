// Package mcp implements Model Context Protocol clients (SPEC §15):
// stdio + HTTP JSON-RPC transports, lazy server lifecycle with idle
// timeout, and the progressive-disclosure surface (names → schemas →
// call) that keeps MCP tool schemas out of the base system prompt.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
)

// ErrNotConnected reports a transport failure.
var ErrNotConnected = errors.New("mcp: server not reachable")

// rpcError is a JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("mcp: rpc error %d: %s", e.Code, e.Message) }

// rpcMsg is one JSON-RPC 2.0 message (request, response, or notification).
type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// transport carries rpc messages both ways.
type transport interface {
	// send writes a request and returns the matching response.
	send(ctx context.Context, m rpcMsg) (rpcMsg, error)
	// notify sends a message with no response expected.
	notify(ctx context.Context, m rpcMsg) error
	close() error
}

// client is one connected MCP server speaking the protocol subset.
type client struct {
	t   transport
	id  atomic.Int64
	ver string
}

const protocolVersion = "2024-11-05"

// handshake performs initialize + notifications/initialized.
func (c *client) handshake(ctx context.Context) error {
	params, _ := json.Marshal(map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "genisys", "version": "0.1"},
	})
	resp, err := c.send(ctx, "initialize", params)
	if err != nil {
		return err
	}
	var out struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return fmt.Errorf("mcp: bad initialize result: %w", err)
	}
	c.ver = out.ProtocolVersion
	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
		return err
	}
	return nil
}

func (c *client) send(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	m := rpcMsg{JSONRPC: "2.0", ID: c.id.Add(1), Method: method, Params: params}
	resp, err := c.t.send(ctx, m)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

func (c *client) notify(ctx context.Context, method string, params json.RawMessage) error {
	return c.t.notify(ctx, rpcMsg{JSONRPC: "2.0", Method: method, Params: params})
}

// --- protocol payloads -------------------------------------------------------

// ToolDef is one MCP tool (SPEC §15 progressive disclosure).
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	Annotations struct {
		ReadOnlyHint *bool `json:"readOnlyHint,omitempty"`
	} `json:"annotations,omitempty"`
}

// CallResult is a tools/call outcome.
type CallResult struct {
	Text    string
	IsError bool
}

func (c *client) listTools(ctx context.Context) ([]ToolDef, error) {
	raw, err := c.send(ctx, "tools/list", json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []ToolDef `json:"tools"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("mcp: bad tools/list: %w", err)
	}
	return out.Tools, nil
}

func (c *client) callTool(ctx context.Context, name string, args json.RawMessage) (CallResult, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	params, _ := json.Marshal(map[string]any{"name": name, "arguments": json.RawMessage(args)})
	raw, err := c.send(ctx, "tools/call", params)
	if err != nil {
		return CallResult{}, err
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return CallResult{}, fmt.Errorf("mcp: bad tools/call: %w", err)
	}
	var res CallResult
	for _, ct := range out.Content {
		if ct.Type == "text" {
			res.Text += ct.Text
		} else {
			res.Text += fmt.Sprintf("[%s content omitted]", ct.Type)
		}
	}
	res.IsError = out.IsError
	return res, nil
}

// writeLine helpers.
func encode(w io.Writer, m rpcMsg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}
