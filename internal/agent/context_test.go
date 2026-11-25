package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/larsn03/genisys/internal/agent/events"
	"github.com/larsn03/genisys/internal/model"
	"github.com/larsn03/genisys/internal/store"
)

func bigSession(n int) []model.Message {
	msgs := []model.Message{{Role: "user", Content: "build the thing"}}
	for i := 0; i < n; i++ {
		msgs = append(msgs,
			model.Message{Role: "assistant", Content: "working", ToolCalls: []model.ToolCall{
				{ID: "t" + nid(i), Type: "function", Function: model.ToolCallFunction{Name: "list_dir", Arguments: `{}`}}}},
			model.Message{Role: "tool", ToolCallID: "t" + nid(i), Content: strings.Repeat("file list ", 200)},
			model.Message{Role: "assistant", Content: "step " + itoa(i) + " done"},
		)
	}
	return msgs
}

func nid(i int) string { b, _ := json.Marshal(i); return string(b) }

func TestAgingKeepsPairing(t *testing.T) {
	msgs := bigSession(10)
	aged := ageToolResults(msgs)
	if len(aged) != len(msgs) {
		t.Fatal("aging must not drop messages")
	}
	var toolIDs, callIDs []string
	for _, m := range aged {
		if m.Role == "tool" {
			toolIDs = append(toolIDs, m.ToolCallID)
		}
		for _, tc := range m.ToolCalls {
			callIDs = append(callIDs, tc.ID)
		}
	}
	if strings.Join(toolIDs, ",") != strings.Join(callIDs, ",") {
		t.Error("aging broke call/result pairing")
	}
	// old results replaced, recent kept
	if aged[2].Content != agedPlaceholder {
		t.Error("old tool result not aged")
	}
	last := aged[len(aged)-1]
	_ = last
	if aged[len(aged)-2].Content == agedPlaceholder {
		t.Error("recent tool result wrongly aged")
	}
}

func TestCompactReplacesHistory(t *testing.T) {
	mock := newMock(t, sseText("## Goal\nbuild the thing\n## Files\nnone"))
	var evs []events.Event
	s := NewSession(Deps{
		Client: mock.client(t), Model: "m", Tools: subagentTools(),
		Events:        events.SinkFunc(func(e events.Event) { evs = append(evs, e) }),
		ContextWindow: 1000,
	})
	s.messages = bigSession(20)
	s.lastPromptTokens = 900 // over the 80% line

	if !s.maybeCompact(context.Background()) {
		t.Fatal("compact did not trigger")
	}
	if s.messages[0].Role != "user" || !strings.HasPrefix(s.messages[0].Content, "[Context summary") {
		t.Fatalf("summary marker missing: %q", s.messages[0].Content)
	}
	if !strings.Contains(s.messages[0].Content, "build the thing") {
		t.Error("summary content missing")
	}
	// tail must not start with an orphan tool message
	if s.messages[1].Role == "tool" {
		t.Error("tail starts with orphan tool result")
	}
	// every tool result in the tail has its assistant call present
	seen := map[string]bool{}
	for _, m := range s.messages {
		for _, tc := range m.ToolCalls {
			seen[tc.ID] = true
		}
	}
	for _, m := range s.messages {
		if m.Role == "tool" && !seen[m.ToolCallID] {
			t.Fatalf("orphaned tool result %s", m.ToolCallID)
		}
	}
	var found bool
	for _, e := range evs {
		if e.Type == "context_compacted" {
			found = true
			p := e.Payload.(map[string]any)
			if p["reason"] != "threshold" || p["before_tokens"].(int) != 900 {
				t.Errorf("payload=%v", p)
			}
		}
	}
	if !found {
		t.Error("context_compacted event not emitted")
	}
}

func TestCompactResumeEquivalence(t *testing.T) {
	dir := t.TempDir()
	tr, err := store.OpenTranscript(dir + "/transcript.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	mock := newMock(t, sseText("## Goal\ncompact me"))
	s := NewSession(Deps{
		Client: mock.client(t), Model: "m", Tools: subagentTools(), Transcript: tr,
	})
	s.messages = bigSession(8)
	if !s.Compact(context.Background(), "manual") {
		t.Fatal("compact failed")
	}
	// more messages after compaction
	s.appendMsg("", model.Message{Role: "assistant", Content: "post-compact"})
	tr.Close()

	mock2 := newMock(t)
	r, err := store.OpenTranscript(dir + "/transcript.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	res, err := Resume(Deps{Client: mock2.client(t), Model: "m", Tools: subagentTools(), Transcript: r})
	if err != nil {
		t.Fatal(err)
	}
	live, resumed := s.Messages(), res.Messages()
	if len(live) != len(resumed) {
		t.Fatalf("resume mismatch: live=%d resumed=%d", len(live), len(resumed))
	}
	for i := range live {
		if live[i].Role != resumed[i].Role || live[i].Content != resumed[i].Content {
			t.Fatalf("msg %d differs: %q vs %q", i, live[i].Content, resumed[i].Content)
		}
	}
}

func TestOverflowClassification(t *testing.T) {
	ae := &model.APIError{Status: 400, Kind: model.KindRequest, Message: "maximum context length exceeded"}
	if !isOverflow(ae) {
		t.Error("overflow not detected")
	}
	if isOverflow(&model.APIError{Status: 401, Kind: model.KindAuth, Message: "bad key"}) {
		t.Error("auth misclassified as overflow")
	}
	if isOverflow(&model.APIError{Status: 400, Kind: model.KindRequest, Message: "invalid tool name"}) {
		t.Error("generic 400 misclassified")
	}
}

func TestOverflowAutoCompactRetry(t *testing.T) {
	// scripted: step call → HTTP 400 overflow; summary call → summary;
	// retry → final answer. The loop must compact and retry once.
	mock := newMock(t,
		status400("This model's maximum context length is 4096 tokens, your prompt has 5000"),
		sseText("## Goal\nrecover from overflow"),
		sseText("recovered"),
	)
	s := NewSession(Deps{
		Client: mock.client(t), Model: "m", Tools: subagentTools(),
		ContextWindow: 1000,
	})
	s.messages = bigSession(30)
	s.lastPromptTokens = 950
	out, err := s.Run(context.Background(), "continue")
	if err != nil {
		t.Fatalf("auto-compact-retry failed: %v", err)
	}
	if out != "recovered" {
		t.Errorf("out=%q", out)
	}
	if mock.requestCount() != 3 {
		t.Errorf("requests=%d want 3 (overflow, summary, retry)", mock.requestCount())
	}
	if !mock.messagesContain(2, "Context summary") {
		t.Error("retry did not carry the summary")
	}
}
