package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/larsn03/genisys/internal/agent/events"
	"github.com/larsn03/genisys/internal/model"
	"github.com/larsn03/genisys/internal/tools"
	exectool "github.com/larsn03/genisys/internal/tools/exec"
	"github.com/larsn03/genisys/internal/tools/fileio"
	"github.com/larsn03/genisys/internal/tools/search"
)

// --- mock endpoint -----------------------------------------------------------

type mockSrv struct {
	mu        sync.Mutex
	responses []string
	captured  []map[string]any
	idx       int
	srv       *httptest.Server
	hook      func(responseIndex int)
}

func newMock(t *testing.T, responses ...string) *mockSrv {
	m := &mockSrv{responses: responses}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var raw map[string]any
		json.Unmarshal(body, &raw)
		m.mu.Lock()
		m.captured = append(m.captured, raw)
		n := m.idx
		if n >= len(m.responses) {
			n = len(m.responses) - 1
		}
		m.idx++
		resp := m.responses[n]
		hook := m.hook
		m.mu.Unlock()
		if hook != nil {
			hook(n)
		}
		if strings.HasPrefix(resp, "STATUS:400:") {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"message":`+strings.TrimPrefix(resp, "STATUS:400:")+`}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, resp)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockSrv) client(t *testing.T) *model.Client {
	t.Helper()
	c, err := model.New(m.srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (m *mockSrv) requestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.captured)
}

func (m *mockSrv) request(i int) map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.captured[i]
}

func (m *mockSrv) messagesContain(i int, sub string) bool {
	b, _ := json.Marshal(m.request(i)["messages"])
	return strings.Contains(string(b), sub)
}

// status400 makes the mock answer HTTP 400 with an OpenAI-style error body.
func status400(msg string) string {
	b, _ := json.Marshal(msg)
	return "STATUS:400:" + string(b)
}

func sseText(text string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": text}}}})
	return "data: " + string(b) + "\n\ndata: [DONE]\n\n"
}

func sseToolCall(id, name, args string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
		"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function",
			"function": map[string]string{"name": name, "arguments": args}}},
	}}}})
	return "data: " + string(b) + "\n\ndata: [DONE]\n\n"
}

func fixtureRegistry() *tools.Registry {
	r := tools.NewRegistry()
	r.Add(fileio.ReadLines{})
	r.Add(fileio.ReadChars{})
	r.Add(fileio.ListDir{})
	r.Add(search.SearchLine{})
	r.Add(search.FindFiles{})
	r.Add(exectool.Shell{})
	return r
}

func fixtureCwd() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "testdata", "fixturerepo")
}

func newSession(t *testing.T, m *mockSrv, sink events.Sink, mode string, maxSteps int) *Session {
	t.Helper()
	return NewSession(Deps{
		Client: m.client(t), Model: "test-model", Tools: fixtureRegistry(),
		ToolsMode: mode, MaxSteps: maxSteps, Cwd: fixtureCwd(),
		Scratchpad: t.TempDir(), Events: sink,
		Retry: &model.RetryPolicy{MaxAttempts: 1, Base: time.Millisecond},
	})
}

// --- tests --------------------------------------------------------------------

func TestNativeToolFlow(t *testing.T) {
	m := newMock(t,
		sseToolCall("call_1", "list_dir", `{"path": ""}`),
		sseText("All done."))
	s := newSession(t, m, nil, "native", 8)

	got, err := s.Run(context.Background(), "list the repo")
	if err != nil {
		t.Fatal(err)
	}
	if got != "All done." {
		t.Errorf("final = %q", got)
	}
	if m.requestCount() != 2 {
		t.Fatalf("requests = %d", m.requestCount())
	}
	// request 2 must carry the tool result (P1-6 DoD: transcript integrity)
	if !m.messagesContain(1, `"role":"tool"`) {
		t.Error("second request missing tool result")
	}
	if !m.messagesContain(1, "D ignored") {
		t.Error("tool result missing fixture content")
	}
	// native mode: tools sent in request
	if _, ok := m.request(0)["tools"]; !ok {
		t.Error("native mode must send tools array")
	}
	msgs := s.Messages()
	if len(msgs) != 4 { // user, assistant+calls, tool, assistant
		t.Fatalf("transcript = %+v", msgs)
	}
}

func TestXMLFallback(t *testing.T) {
	m := newMock(t,
		sseText("Here you go.\n<list_dir>{\"path\": \"\"}</list_dir>"),
		sseText("done"))
	s := newSession(t, m, nil, "xml", 8)
	got, err := s.Run(context.Background(), "list")
	if err != nil {
		t.Fatal(err)
	}
	if got != "done" {
		t.Errorf("final = %q", got)
	}
	if !m.messagesContain(1, "D ignored") {
		t.Error("XML call was not executed")
	}
	if _, ok := m.request(0)["tools"]; ok {
		t.Error("xml mode must not send tools array")
	}
	// prose outside the call was preserved in the transcript
	if !strings.Contains(fmt.Sprint(s.Messages()[1].Content), "Here you go.") {
		t.Errorf("assistant prose: %q", s.Messages()[1].Content)
	}
}

func TestQueueInterjectionDeliveredAtBoundary(t *testing.T) {
	m := newMock(t,
		sseToolCall("c1", "list_dir", `{"path": ""}`),
		sseText("noted"))
	s := newSession(t, m, nil, "native", 8)
	// push right after the tool finishes (synchronous sink = before the
	// loop re-enters its boundary drain)
	var once sync.Once
	s.d.Events = events.SinkFunc(func(e events.Event) {
		if e.Type == events.ToolFinished {
			once.Do(func() {
				s.Interjections().Push("also check src/", ModeQueue)
			})
		}
	})
	got, err := s.Run(context.Background(), "list")
	if err != nil {
		t.Fatal(err)
	}
	if got != "noted" {
		t.Errorf("final = %q", got)
	}
	if !m.messagesContain(1, "[user interjection") || !m.messagesContain(1, "also check src/") {
		t.Errorf("queued interjection not injected at boundary; req1=%v", m.request(1)["messages"])
	}
}

func TestInterruptKillsToolAndHonorsNewInstruction(t *testing.T) {
	// P1-9 DoD: /steer mid-shell_command kills the process group, the
	// workspace stays consistent and the new instruction is honored first.
	m := newMock(t,
		sseToolCall("c1", "shell_command", `{"command":"touch interrupted.marker; sleep 20; touch never.marker","timeout_ms":15000}`),
		sseText("redirected"))
	s := newSession(t, m, nil, "native", 8)
	s.d.Cwd = t.TempDir() // isolated: this test writes into the workspace
	var once sync.Once
	s.d.Events = events.SinkFunc(func(e events.Event) {
		if e.Type == events.ToolStarted {
			once.Do(func() {
				go func() {
					time.Sleep(400 * time.Millisecond) // let touch run, sleep start
					s.Interjections().Push("stop, do something else", ModeInterrupt)
				}()
			})
		}
	})
	start := time.Now()
	got, err := s.Run(context.Background(), "long task")
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("sleep not killed: %v", elapsed)
	}
	if got != "redirected" {
		t.Errorf("final = %q", got)
	}
	if !m.messagesContain(1, InterruptedResult) && !m.messagesContain(1, "[interrupted") {
		t.Error("missing interrupted tool result (synthetic or partial-output)")
	}
	if !m.messagesContain(1, "stop, do something else") {
		t.Error("interrupt text not injected")
	}
	// workspace: first touch ran, post-sleep touch must not exist
	cwd := s.d.Cwd
	if _, err := os.Stat(filepath.Join(cwd, "never.marker")); err == nil {
		t.Error("process group survived the interrupt")
	}
	if _, err := os.Stat(filepath.Join(cwd, "interrupted.marker")); err != nil {
		t.Error("pre-sleep side effect should have happened")
	}
}

func TestStepBudgetWrapUp(t *testing.T) {
	m := newMock(t,
		sseToolCall("c1", "list_dir", `{"path": ""}`),
		sseText("SUMMARY: done X, remaining Y"))
	s := newSession(t, m, nil, "native", 1) // only one tool step allowed
	got, err := s.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "SUMMARY") {
		t.Errorf("wrap-up = %q", got)
	}
	if !m.messagesContain(1, "Step budget exhausted") {
		t.Error("missing budget message")
	}
	if _, ok := m.request(1)["tools"]; ok {
		t.Error("wrap-up request must not offer tools")
	}
}

func TestLoopDetectionNudge(t *testing.T) {
	same := sseToolCall("c1", "list_dir", `{"path": ""}`)
	m := newMock(t, same, same, same, sseText("gave up"))
	s := newSession(t, m, nil, "native", 10)
	if _, err := s.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if !m.messagesContain(3, "[harness] You have repeated") {
		t.Error("loop-detection nudge missing")
	}
}

// --- P5-1: sub-agent (SPEC §12) ----------------------------------------------

func TestSubagentLocator(t *testing.T) {
	// fixture "monorepo"
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "internal/http"), 0o755)
	os.WriteFile(filepath.Join(dir, "internal/http/client.go"),
		[]byte("package http\n\n// Client dials the API.\ntype Client struct{}\n"), 0o644)

	// scripted locator: search → final answer with citation
	mock := newMock(t,
		sseToolCall("t1", "search_file_line", `{"pattern":"dials the API","path":"."}`),
		sseText("Found the HTTP client.\n\nCitations:\n- internal/http/client.go:3 — doc comment of Client"))
	var evs []events.Event
	parent := Deps{
		Client: mock.client(t), Model: "m", Cwd: dir,
		Tools:     subagentTools(),
		Events:    events.SinkFunc(func(e events.Event) { evs = append(evs, e) }),
		ToolsMode: "none", // XML fallback: the mock emits <search_file_line> tags? no — native calls need tools; keep native below
	}
	parent.ToolsMode = "native"
	parent.Tools = subagentTools()
	out := SubagentQuery(context.Background(), parent, "internal/http", "the HTTP client", "step_9")
	if !strings.Contains(out, "internal/http/client.go:3") {
		t.Fatalf("locator output missing citation: %q", out)
	}
	// restricted toolset in the request (SPEC §9.4: exactly 4 tools)
	req := mock.request(0)
	toolsJSON, _ := json.Marshal(req["tools"])
	for _, want := range []string{"search_file_line", "find_files", "list_dir", "read_file_lines"} {
		if !strings.Contains(string(toolsJSON), want) {
			t.Errorf("subagent tools missing %s: %s", want, toolsJSON)
		}
	}
	if n := strings.Count(string(toolsJSON), `"name"`); n != 4 {
		t.Errorf("subagent toolset not restricted: %d tools", n)
	}
	// locator system prompt reached the model
	if !mock.messagesContain(0, "code locator") {
		t.Error("locator system prompt missing")
	}
	// nested trace attribution
	var nested int
	for _, e := range evs {
		m, _ := e.Payload.(map[string]any)
		if m != nil && m["subagent"] == true && m["parent_step"] == "step_9" {
			nested++
		}
	}
	if nested == 0 {
		t.Error("sub-agent events not tagged for nested trace")
	}
}

func TestSubagentFailureIsolation(t *testing.T) {
	mock := newMock(t, sseText("unused"))
	c := mock.client(t)
	// point at a dead port: every completion fails
	dead, err := model.New("http://127.0.0.1:1/v1", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = c
	out := SubagentQuery(context.Background(), Deps{
		Client: dead, Model: "m", Cwd: t.TempDir(), Tools: subagentTools(),
		Retry: &model.RetryPolicy{MaxAttempts: 1, Base: time.Millisecond},
	}, ".", "anything", "s1")
	if !strings.HasPrefix(out, "[query failed:") {
		t.Fatalf("expected failure string, got %q", out)
	}
}
