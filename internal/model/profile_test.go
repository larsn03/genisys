package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// chunkJSON builds one SSE chunk line from a delta map.
func chunkJSON(delta map[string]any) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta}}})
	return "data: " + string(b) + "\n\n"
}

func TestProbeNative(t *testing.T) {
	var sawTools bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &req)
		if len(req.Tools) > 0 {
			sawTools = true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, chunkJSON(map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": "call_1", "type": "function",
			"function": map[string]string{"name": "probe_echo", "arguments": `{"text":"ping"}`},
		}}}))
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"total_tokens\":9}}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "")
	res, err := c.Probe(context.Background(), "m")
	if err != nil {
		t.Fatal(err)
	}
	if res.Tools != "native" || !res.Usage {
		t.Errorf("res = %+v", res)
	}
	if !sawTools {
		t.Error("probe should send tools in at least one request")
	}
}

func TestProbeXMLInText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, chunkJSON(map[string]any{"content": `Sure. <probe_echo>{"text": "ping"}</probe_echo>`}))
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "")
	res, err := c.Probe(context.Background(), "m")
	if err != nil {
		t.Fatal(err)
	}
	if res.Tools != "xml" {
		t.Errorf("Tools = %q, want xml (%+v)", res.Tools, res.Notes)
	}
}

func TestProbeNone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, chunkJSON(map[string]any{"content": "I would call the tool."}))
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "")
	res, err := c.Probe(context.Background(), "m")
	if err != nil {
		t.Fatal(err)
	}
	if res.Tools != "none" {
		t.Errorf("Tools = %q", res.Tools)
	}
}

// endpoint that 400s on the tools field → probe bisects to rejects_tools.
func TestProbeBisectsQuirks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &raw)
		if _, hasTools := raw["tools"]; hasTools {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"tools not supported"}`)
			return
		}
		if _, hasMT := raw["max_tokens"]; hasMT {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"max_tokens not supported"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, chunkJSON(map[string]any{"content": "plain"}))
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "")
	res, err := c.Probe(context.Background(), "m")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Quirks, ",")
	if !strings.Contains(joined, QuirkDropMaxTokens) || !strings.Contains(joined, QuirkRejectsTools) {
		t.Errorf("quirks = %v", res.Quirks)
	}
	if res.Tools != "none" {
		t.Errorf("Tools = %q", res.Tools)
	}
}

func TestQuirksAppliedToRequests(t *testing.T) {
	var sawStreamOptions, sawMaxTokens bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &raw)
		_, sawStreamOptions = raw["stream_options"]
		_, sawMaxTokens = raw["max_tokens"]
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, chunkJSON(map[string]any{"content": "hi  <think>deep thoughts</think> "}))
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "", WithQuirks(QuirkRejectsStreamOptions, QuirkDropMaxTokens, QuirkEmitsThinkTags))
	mt := 16
	comp, err := c.Complete(context.Background(), ChatRequest{Model: "m", MaxTokens: &mt})
	if err != nil {
		t.Fatal(err)
	}
	if sawStreamOptions {
		t.Error("stream_options should be omitted (quirk)")
	}
	if sawMaxTokens {
		t.Error("max_tokens should be omitted (quirk)")
	}
	if comp.Text != "hi" {
		t.Errorf("think tags not stripped: %q", comp.Text)
	}
}
