package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/larsn03/genisys/internal/api"
	"github.com/larsn03/genisys/internal/app"
)

// --- scripted mock OpenAI endpoint ---------------------------------------

type mockModel struct {
	srv     *httptest.Server
	mu      sync.Mutex
	scripts [][]string // per-session scripts are overkill; one global queue
	queue   []string
	delay   time.Duration
	bodies  []string // captured request bodies
}

// body returns all captured request bodies concatenated.
func (m *mockModel) body() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.bodies, "\n")
}

func newMockModel(t *testing.T, responses []string, delay time.Duration) *mockModel {
	m := &mockModel{queue: responses, delay: delay}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.delay > 0 {
			time.Sleep(m.delay)
		}
		m.mu.Lock()
		b, _ := io.ReadAll(r.Body)
		m.bodies = append(m.bodies, string(b))
		resp := ""
		if len(m.queue) > 0 {
			resp = m.queue[0]
			if len(m.queue) > 1 {
				m.queue = m.queue[1:]
			}
		} else {
			resp = sseText("fallback answer")
		}
		m.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, resp)
	}))
	t.Cleanup(m.srv.Close)
	return m
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

// --- harness -----------------------------------------------------------------

type env struct {
	app    *app.App
	srv    *api.Server
	hs     *httptest.Server
	mock   *mockModel
	wsdir  string
	client *http.Client
	token  string
}

func newEnv(t *testing.T, responses []string, delay time.Duration) *env {
	t.Helper()
	mock := newMockModel(t, responses, delay)
	tmp := t.TempDir()
	wsdir := filepath.Join(tmp, "ws")
	os.MkdirAll(wsdir, 0o755)
	cfgPath := filepath.Join(tmp, "config.yaml")
	cfg := fmt.Sprintf(`version: 1
default_model: mock/m1
endpoints:
  - id: mock
    base_url: %s/v1
    models:
      - id: m1
`, mock.srv.URL)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(cfgPath, filepath.Join(tmp, "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	srv := api.New(a, cfgPath, nil, log.New(io.Discard, "", 0))
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return &env{app: a, srv: srv, hs: hs, wsdir: wsdir, client: hs.Client(), token: srv.Token, mock: mock}
}

func (e *env) do(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.hs.URL+path, rdr)
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (e *env) mustJSON(t *testing.T, method, path string, body any, out any) {
	t.Helper()
	code, b := e.do(t, method, path, body)
	if code >= 300 {
		t.Fatalf("%s %s → %d: %s", method, path, code, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("decode %s %s: %v (%s)", method, path, err, b)
		}
	}
}

func (e *env) newSession(t *testing.T) string {
	t.Helper()
	// project on the env workspace
	var proj struct {
		ID string `json:"id"`
	}
	e.mustJSON(t, "POST", "/api/projects", map[string]any{
		"name": "proj", "kind": "code", "workspace_path": e.wsdir, "allow_coding": true,
	}, &proj)
	var sess struct {
		ID string `json:"id"`
	}
	e.mustJSON(t, "POST", "/api/sessions", map[string]any{
		"project_id": proj.ID, "model_ref": "mock/m1",
	}, &sess)
	return sess.ID
}

// waitFor polls fn until it returns true or the deadline passes.
func waitFor(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

type evRow struct {
	ID          int64  `json:"id"`
	StepID      string `json:"step_id"`
	Type        string `json:"type"`
	PayloadJSON string `json:"payload_json"`
	CreatedAt   string `json:"created_at"`
}

func (e *env) events(t *testing.T, session string, after int64) []evRow {
	t.Helper()
	var rows []evRow
	e.mustJSON(t, "GET", fmt.Sprintf("/api/sessions/%s/events?after=%d", session, after), nil, &rows)
	return rows
}

func (e *env) waitEvent(t *testing.T, session, typ string) evRow {
	t.Helper()
	var got evRow
	waitFor(t, "event "+typ, func() bool {
		for _, ev := range e.events(t, session, 0) {
			if ev.Type == typ {
				got = ev
				return true
			}
		}
		return false
	})
	return got
}

func (e *env) waitIdle(t *testing.T, session string) {
	t.Helper()
	waitFor(t, "session idle", func() bool {
		for _, ev := range e.events(t, session, 0) {
			if ev.Type == "session_status" && strings.Contains(ev.PayloadJSON, "idle") {
				return true
			}
		}
		return false
	})
}

// --- tests ---------------------------------------------------------------------

func TestAuth(t *testing.T) {
	e := newEnv(t, []string{sseText("x")}, 0)
	req, _ := http.NewRequest("GET", e.hs.URL+"/api/sessions", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no-token status = %d", resp.StatusCode)
	}
	// version is open
	resp2, err := http.Get(e.hs.URL + "/api/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Errorf("version status = %d", resp2.StatusCode)
	}
}

func TestCRUD(t *testing.T) {
	e := newEnv(t, []string{sseText("x")}, 0)
	var sess struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	e.mustJSON(t, "POST", "/api/sessions", map[string]any{"model_ref": "mock/m1", "title": "fix bug"}, &sess)
	if sess.ID == "" || sess.Title != "fix bug" {
		t.Fatalf("session = %+v", sess)
	}
	e.mustJSON(t, "PATCH", "/api/sessions/"+sess.ID, map[string]any{"pinned": true}, nil)
	var rows []map[string]any
	e.mustJSON(t, "GET", "/api/sessions", nil, &rows)
	var found bool
	for _, r := range rows {
		if r["id"] == sess.ID && r["pinned"] == true {
			found = true
		}
	}
	if !found {
		t.Error("pin not persisted")
	}
	e.mustJSON(t, "DELETE", "/api/sessions/"+sess.ID, nil, nil)
	e.mustJSON(t, "GET", "/api/sessions/"+sess.ID, nil, nil)
}

func TestModelsAndGitStatus(t *testing.T) {
	e := newEnv(t, []string{sseText("x")}, 0)
	var models struct {
		Default string `json:"default"`
		Models  []struct {
			Ref string `json:"ref"`
		} `json:"models"`
	}
	e.mustJSON(t, "GET", "/api/models", nil, &models)
	if models.Default != "mock/m1" || len(models.Models) != 1 || models.Models[0].Ref != "mock/m1" {
		t.Fatalf("models = %+v", models)
	}
	sid := e.newSession(t)
	// switch model via PATCH
	e.mustJSON(t, "PATCH", "/api/sessions/"+sid, map[string]any{"model_ref": "mock/m1"}, nil)
	// git badges: init a repo in the workspace
	var sess struct {
		ProjectID string `json:"project_id"`
	}
	e.mustJSON(t, "GET", "/api/sessions/"+sid, nil, &struct {
		Session map[string]any `json:"session"`
	}{})
	_ = sess
	var gs struct {
		Repo   bool              `json:"repo"`
		Branch string            `json:"branch"`
		Files  map[string]string `json:"files"`
	}
	e.mustJSON(t, "GET", "/api/sessions/"+sid+"/git-status", nil, &gs)
	if gs.Repo {
		t.Fatalf("expected repo:false for plain dir, got %+v", gs)
	}
}

// P4-6: preview serves text directly and office docs via LibreOffice.
func TestPreview(t *testing.T) {
	e := newEnv(t, []string{sseText("x")}, 0)
	sid := e.newSession(t)
	os.WriteFile(filepath.Join(e.wsdir, "note.md"), []byte("# hi"), 0o644)
	code, b := e.do(t, "GET", "/api/sessions/"+sid+"/preview?path=note.md", nil)
	if code != 200 || !strings.Contains(string(b), "# hi") {
		t.Errorf("md preview = %d %s", code, b)
	}
	// office doc without soffice → 415 fallback; with soffice → pdf
	os.WriteFile(filepath.Join(e.wsdir, "d.docx"), []byte("PK\x03\x04not-really"), 0o644)
	code, b = e.do(t, "GET", "/api/sessions/"+sid+"/preview?path=d.docx", nil)
	if _, err := exec.LookPath("soffice"); err != nil {
		if code != http.StatusUnsupportedMediaType {
			t.Errorf("docx preview fallback = %d, want 415: %s", code, b)
		}
	} else {
		if code != 200 && code != http.StatusUnsupportedMediaType {
			t.Errorf("docx preview = %d: %s", code, b)
		}
	}
	// scratchpad fallback: upload then preview by bare name
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "up.md")
	fw.Write([]byte("uploaded"))
	mw.Close()
	req, _ := http.NewRequest("POST", e.hs.URL+"/api/sessions/"+sid+"/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	code, b = e.do(t, "GET", "/api/sessions/"+sid+"/preview?path=up.md", nil)
	if code != 200 || !strings.Contains(string(b), "uploaded") {
		t.Errorf("scratchpad preview = %d %s", code, b)
	}
}

// Attachments: multipart upload lands in the session scratchpad.
func TestUpload(t *testing.T) {
	e := newEnv(t, []string{sseText("x")}, 0)
	sid := e.newSession(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "notes.txt")
	fw.Write([]byte("attached!"))
	mw.Close()
	req, _ := http.NewRequest("POST", e.hs.URL+"/api/sessions/"+sid+"/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload = %d: %s", resp.StatusCode, b)
	}
	var res struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	json.NewDecoder(resp.Body).Decode(&res)
	if !strings.HasSuffix(res.Path, filepath.Join("attachments", "notes.txt")) {
		t.Errorf("path = %s", res.Path)
	}
	b, err := os.ReadFile(res.Path)
	if err != nil || string(b) != "attached!" {
		t.Errorf("file = %q (%v)", b, err)
	}
	// second upload dedupes instead of clobbering
	req2, _ := http.NewRequest("POST", e.hs.URL+"/api/sessions/"+sid+"/upload", bytes.NewReader(buf.Bytes()))
	req2.Header.Set("Content-Type", mw.FormDataContentType())
	req2.Header.Set("Authorization", "Bearer "+e.token)
	resp2, err := e.client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var res2 struct {
		Path string `json:"path"`
	}
	json.NewDecoder(resp2.Body).Decode(&res2)
	if res2.Path == res.Path {
		t.Error("second upload clobbered the first")
	}
}

// P3-1: prompt → tools → events ledger → messages, all over REST.
func TestPromptFlow(t *testing.T) {
	e := newEnv(t, []string{
		sseToolCall("c1", "list_dir", `{"path": ""}`),
		sseText("all done"),
	}, 0)
	sid := e.newSession(t)
	var res map[string]string
	e.mustJSON(t, "POST", "/api/sessions/"+sid+"/prompt", map[string]any{"text": "list it"}, &res)
	if res["state"] != "started" {
		t.Fatalf("state = %v", res)
	}
	e.waitIdle(t, sid)

	types := map[string]bool{}
	for _, ev := range e.events(t, sid, 0) {
		types[ev.Type] = true
	}
	for _, want := range []string{"user_turn", "step_started", "tool_started", "tool_finished", "text_delta", "session_status"} {
		if !types[want] {
			t.Errorf("missing event %s (have %v)", want, types)
		}
	}
	var msgs []map[string]any
	e.mustJSON(t, "GET", "/api/sessions/"+sid+"/messages", nil, &msgs)
	if len(msgs) != 4 {
		t.Errorf("messages = %d", len(msgs))
	}
}

// P3-5: permission card round-trip over REST.
func TestPermissionFlow(t *testing.T) {
	e := newEnv(t, []string{
		sseToolCall("c1", "request_readonly_access", `{"path": "/etc/hostname", "reason": "testing"}`),
		sseText("read it"),
	}, 0)
	sid := e.newSession(t)
	e.mustJSON(t, "POST", "/api/sessions/"+sid+"/prompt", map[string]any{"text": "read outside"}, nil)

	pe := e.waitEvent(t, sid, "permission_requested")
	var payload map[string]any
	json.Unmarshal([]byte(pe.PayloadJSON), &payload)
	pid := int64(payload["id"].(float64))

	// answer
	e.mustJSON(t, "POST", fmt.Sprintf("/api/sessions/%s/permissions/%d", sid, pid), map[string]any{"granted": true}, nil)
	e.waitIdle(t, sid)

	var msgs []map[string]any
	e.mustJSON(t, "GET", "/api/sessions/"+sid+"/messages", nil, &msgs)
	var granted bool
	for _, m := range msgs {
		if strings.Contains(fmt.Sprint(m["content_json"]), "access granted") {
			granted = true
		}
	}
	if !granted {
		t.Error("permission grant did not reach the tool result")
	}
}

// P3-4: interrupt interjection over REST.
func TestInterruptOverREST(t *testing.T) {
	e := newEnv(t, []string{
		sseToolCall("c1", "shell_command", `{"command":"sleep 5","timeout_ms":9000}`),
		sseText("redirected"),
	}, 0)
	sid := e.newSession(t)
	e.mustJSON(t, "POST", "/api/sessions/"+sid+"/prompt", map[string]any{"text": "long task"}, nil)
	// wait until the sleep is actually running (tool_started for shell_command)
	waitFor(t, "shell running", func() bool {
		for _, ev := range e.events(t, sid, 0) {
			if ev.Type == "tool_started" && strings.Contains(ev.PayloadJSON, "shell_command") {
				return true
			}
		}
		return false
	})
	time.Sleep(300 * time.Millisecond) // let exec start the process

	e.mustJSON(t, "POST", "/api/sessions/"+sid+"/prompt", map[string]any{"text": "stop!", "mode": "interrupt"}, nil)
	e.waitIdle(t, sid)

	types := map[string]bool{}
	for _, ev := range e.events(t, sid, 0) {
		types[ev.Type] = true
	}
	for _, want := range []string{"interjection_queued", "interrupt_started", "interjection_delivered"} {
		if !types[want] {
			t.Errorf("missing %s", want)
		}
	}
	var msgs []map[string]any
	e.mustJSON(t, "GET", "/api/sessions/"+sid+"/messages", nil, &msgs)
	var sawIJ, sawInt bool
	for _, m := range msgs {
		c := fmt.Sprint(m["content_json"])
		if strings.Contains(c, "user interjection") {
			sawIJ = true
		}
		if strings.Contains(c, "[interrupted") {
			sawInt = true
		}
	}
	if !sawIJ || !sawInt {
		t.Errorf("transcript missing interjection/interrupted rows (ij=%v int=%v)", sawIJ, sawInt)
	}
}

// P3-1: files endpoints + editor read-before-write.
func TestFilesEndpoints(t *testing.T) {
	e := newEnv(t, []string{sseText("x")}, 0)
	sid := e.newSession(t)
	os.WriteFile(filepath.Join(e.wsdir, "a.txt"), []byte("hello\n"), 0o644)

	var files []map[string]any
	e.mustJSON(t, "GET", "/api/sessions/"+sid+"/files?path=", nil, &files)
	var found bool
	for _, f := range files {
		if f["name"] == "a.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("list files = %v", files)
	}

	// PUT without prior GET → 409
	code, _ := e.do(t, "PUT", "/api/sessions/"+sid+"/file?path=a.txt", map[string]any{"content": "edited\n"})
	if code != http.StatusConflict {
		t.Errorf("PUT-before-GET status = %d", code)
	}
	e.mustJSON(t, "GET", "/api/sessions/"+sid+"/file?path=a.txt", nil, nil)
	e.mustJSON(t, "PUT", "/api/sessions/"+sid+"/file?path=a.txt", map[string]any{"content": "edited\n"}, nil)
	b, _ := os.ReadFile(filepath.Join(e.wsdir, "a.txt"))
	if string(b) != "edited\n" {
		t.Errorf("file = %q", b)
	}
	// checkpoint was created by the editor write
	var cps []map[string]any
	e.mustJSON(t, "GET", "/api/sessions/"+sid+"/checkpoints", nil, &cps)
	if len(cps) == 0 {
		t.Fatal("editor write created no checkpoint")
	}

	// P3-11: revert the editor step → file content comes back
	var rev struct {
		Reverted []string `json:"reverted"`
	}
	e.mustJSON(t, "POST", "/api/sessions/"+sid+"/revert", map[string]any{"step_id": "editor"}, &rev)
	if len(rev.Reverted) != 1 || rev.Reverted[0] != "a.txt" {
		t.Errorf("revert = %+v", rev)
	}
	b, _ = os.ReadFile(filepath.Join(e.wsdir, "a.txt"))
	if string(b) != "hello\n" {
		t.Errorf("after revert file = %q", b)
	}
}

// P3-2: WS replay gap-free + live stream + commands.
func TestWebSocket(t *testing.T) {
	e := newEnv(t, []string{
		sseToolCall("c1", "list_dir", `{"path": ""}`),
		sseText("ws done"),
	}, 0)
	sid := e.newSession(t)

	conn, _, err := websocket.DefaultDialer.Dial(
		strings.Replace(e.hs.URL, "http://", "ws://", 1)+"/ws?session="+sid+"&token="+e.token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	write := func(v any) {
		if err := conn.WriteJSON(v); err != nil {
			t.Fatal(err)
		}
	}
	read := func() api.WSEvent {
		var ev api.WSEvent
		conn.SetReadDeadline(time.Now().Add(8 * time.Second))
		if err := conn.ReadJSON(&ev); err != nil {
			t.Fatalf("read: %v", err)
		}
		return ev
	}

	write(map[string]any{"type": "prompt", "text": "list it", "mode": "queue"})
	var ids []int64
	sawText := false
	for {
		ev := read()
		if ev.ID > 0 {
			ids = append(ids, ev.ID)
		}
		if ev.Type == "text_delta" {
			sawText = true
		}
		if ev.Type == "session_status" {
			if p, ok := ev.Payload["status"].(string); ok && p == "idle" {
				break
			}
		}
	}
	if !sawText {
		t.Error("no text_delta over ws")
	}
	// ids strictly increasing (ledger order)
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("ids not increasing: %v", ids)
		}
	}

	// reconnect with last_event_id = first id → replay must be gap-free:
	// every ledger id in (first, max] arrives exactly once
	last := ids[0]
	conn2, _, err := websocket.DefaultDialer.Dial(
		strings.Replace(e.hs.URL, "http://", "ws://", 1)+
			fmt.Sprintf("/ws?session=%s&token=%s&last_event_id=%d", sid, e.token, last), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()
	var replayed []int64
	sawPayload := false
	for {
		var ev api.WSEvent
		conn2.SetReadDeadline(time.Now().Add(3 * time.Second))
		if err := conn2.ReadJSON(&ev); err != nil {
			break // deadline = replay exhausted
		}
		replayed = append(replayed, ev.ID)
		if ev.Type == "text_delta" && fmt.Sprint(ev.Payload["text"]) == "ws done" {
			sawPayload = true // replayed frames must carry payloads, not just ids
		}
	}
	if !sawPayload {
		t.Error("replayed text_delta lost its payload")
	}
	all := e.events(t, sid, last)
	if len(replayed) != len(all) {
		t.Fatalf("replayed %d, ledger has %d", len(replayed), len(all))
	}
	for i, ev := range all {
		if replayed[i] != ev.ID {
			t.Fatalf("replay gap at %d: got %d want %d", i, replayed[i], ev.ID)
		}
	}
}

// P3-3: concurrency cap 4 + queue.
func TestConcurrencyCap(t *testing.T) {
	responses := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		responses = append(responses, sseText("done"))
	}
	e := newEnv(t, responses, 250*time.Millisecond)
	sids := make([]string, 6)
	for i := range sids {
		sids[i] = e.newSession(t)
	}
	started, queued := 0, 0
	for _, sid := range sids {
		var res map[string]string
		e.mustJSON(t, "POST", "/api/sessions/"+sid+"/prompt", map[string]any{"text": "go"}, &res)
		switch res["state"] {
		case "started":
			started++
		case "queued":
			queued++
		}
	}
	if started != e.srv.Sup.Cap {
		t.Errorf("started = %d, want cap %d", started, e.srv.Sup.Cap)
	}
	if queued != 6-e.srv.Sup.Cap {
		t.Errorf("queued = %d", queued)
	}
	for _, sid := range sids {
		e.waitIdle(t, sid)
	}
	if q := e.srv.Sup.Queued(); q != 0 {
		t.Errorf("queue not drained: %d", q)
	}
}

// --- P5: skills + browser origins -------------------------------------------

func TestSkillsLifecycle(t *testing.T) {
	e := newEnv(t, []string{sseText("ok")}, 0)
	var out struct {
		Skills []struct {
			Name    string `json:"name"`
			Builtin bool   `json:"builtin"`
		} `json:"skills"`
	}
	e.mustJSON(t, "GET", "/api/skills", nil, &out)
	names := map[string]bool{}
	for _, s := range out.Skills {
		names[s.Name] = true
	}
	for _, want := range []string{"skill-management", "documents-presentations-spreadsheets"} {
		if !names[want] {
			t.Errorf("builtin skill %q not listed (have %v)", want, names)
		}
	}

	// install from raw content (URL path is the same code after fetch)
	code, b := e.do(t, "POST", "/api/skills/install", map[string]string{
		"content": "---\nname: my-skill\ndescription: Does a thing.\n---\n\nsteps\n",
	})
	if code != 201 {
		t.Fatalf("install → %d: %s", code, b)
	}
	e.mustJSON(t, "GET", "/api/skills", nil, &out)
	names = map[string]bool{}
	for _, s := range out.Skills {
		names[s.Name] = true
	}
	if !names["my-skill"] {
		t.Error("installed skill not listed")
	}

	// invalid skill → 422
	if code, _ := e.do(t, "POST", "/api/skills/install", map[string]string{"content": "junk"}); code != 422 {
		t.Errorf("invalid install → %d want 422", code)
	}
	// remove user skill
	e.mustJSON(t, "DELETE", "/api/skills/my-skill", nil, nil)
	e.mustJSON(t, "GET", "/api/skills", nil, &out)
	names = map[string]bool{}
	for _, s := range out.Skills {
		names[s.Name] = true
	}
	if names["my-skill"] {
		t.Error("skill survived delete")
	}
	// builtins refuse removal
	if code, _ := e.do(t, "DELETE", "/api/skills/skill-management", nil); code != 422 {
		t.Errorf("builtin delete → %d want 422", code)
	}
}

func TestPromptWithSkills(t *testing.T) {
	e := newEnv(t, []string{sseText("using the skill")}, 0)
	sid := e.newSession(t)
	var res map[string]string
	e.mustJSON(t, "POST", "/api/sessions/"+sid+"/prompt",
		map[string]any{"text": "make me a deck", "skills": []string{"documents-presentations-spreadsheets"}}, &res)
	e.waitIdle(t, sid)
	// the mock endpoint captured the user message with the harness line
	if !strings.Contains(e.mock.body(), "explicitly requested skill") {
		t.Error("skill instruction line not appended to the user turn")
	}
}

func TestBrowserOrigins(t *testing.T) {
	e := newEnv(t, []string{sseText("ok")}, 0)
	var out struct {
		Origins []string `json:"origins"`
	}
	e.mustJSON(t, "GET", "/api/browser/origins", nil, &out)
	if out.Origins == nil || len(out.Origins) != 0 {
		t.Errorf("expected empty non-null origins, got %v", out.Origins)
	}
}

// --- P6-1: fork / pin / hard delete ------------------------------------------

func TestForkAndDelete(t *testing.T) {
	e := newEnv(t, []string{
		sseToolCall("c1", "list_dir", `{"path": ""}`),
		sseText("first answer"),
	}, 0)
	sid := e.newSession(t)
	e.mustJSON(t, "POST", "/api/sessions/"+sid+"/prompt", map[string]any{"text": "go"}, nil)
	e.waitIdle(t, sid)

	// fork
	var fork map[string]any
	e.mustJSON(t, "POST", "/api/sessions/"+sid+"/fork", map[string]any{}, &fork)
	fid := fork["id"].(string)
	if fid == "" || fid == sid {
		t.Fatalf("fork id = %v", fid)
	}
	if fork["forked_from"] != sid {
		t.Errorf("forked_from = %v", fork["forked_from"])
	}
	if !strings.Contains(fork["title"].(string), "fork") {
		t.Errorf("title = %v", fork["title"])
	}
	// fork carries the transcript
	var msgs []map[string]any
	e.mustJSON(t, "GET", "/api/sessions/"+fid+"/messages", nil, &msgs)
	var orig []map[string]any
	e.mustJSON(t, "GET", "/api/sessions/"+sid+"/messages", nil, &orig)
	if len(msgs) != len(orig) || len(msgs) == 0 {
		t.Fatalf("fork messages=%d original=%d", len(msgs), len(orig))
	}
	// fork is independent: prompting the fork must not touch the source
	e2 := []string{sseText("fork answer")}
	_ = e2
	e.mustJSON(t, "POST", "/api/sessions/"+fid+"/prompt", map[string]any{"text": "fork turn"}, nil)
	e.waitIdle(t, fid)
	var msgs2 []map[string]any
	e.mustJSON(t, "GET", "/api/sessions/"+sid+"/messages", nil, &msgs2)
	if len(msgs2) != len(orig) {
		t.Errorf("source grew after fork: %d → %d", len(orig), len(msgs2))
	}

	// pin
	e.mustJSON(t, "PATCH", "/api/sessions/"+sid, map[string]any{"pinned": true}, nil)
	var got struct {
		Session struct {
			Pinned bool `json:"pinned"`
		} `json:"session"`
	}
	e.mustJSON(t, "GET", "/api/sessions/"+sid, nil, &got)
	if !got.Session.Pinned {
		t.Error("pin not persisted")
	}

	// hard delete
	e.mustJSON(t, "DELETE", "/api/sessions/"+fid+"?hard=1", nil, nil)
	if code, _ := e.do(t, "GET", "/api/sessions/"+fid, nil); code != 404 {
		t.Errorf("deleted fork GET → %d want 404", code)
	}
}

// --- P6-3: file history + restore --------------------------------------------

func TestFileHistoryRestore(t *testing.T) {
	e := newEnv(t, []string{
		sseToolCall("c1", "replace_file", `{"path":"note.txt","content":"version one"}`),
		sseToolCall("c2", "read_file_lines", `{"path":"note.txt"}`),
		sseToolCall("c3", "replace_file", `{"path":"note.txt","content":"version two"}`),
		sseText("done"),
	}, 0)
	sid := e.newSession(t)
	e.mustJSON(t, "POST", "/api/sessions/"+sid+"/prompt", map[string]any{"text": "write twice"}, nil)
	e.waitIdle(t, sid)

	// history for the file: SPEC §11 snapshots are PRE-mutation, so the
	// second write's pre-commit captured "version one"
	var hist []map[string]any
	e.mustJSON(t, "GET", "/api/sessions/"+sid+"/file-history?path=note.txt", nil, &hist)
	if len(hist) < 1 {
		t.Fatalf("snapshots=%d want >=1", len(hist))
	}
	oldest := hist[len(hist)-1]["sha"].(string)

	// current content is v2
	_, body := e.do(t, "GET", "/api/sessions/"+sid+"/file?path=note.txt", nil)
	if !strings.Contains(string(body), "version two") {
		t.Fatalf("current=%s", body)
	}
	// snapshot at oldest sha is v1
	code, body := e.do(t, "GET", "/api/sessions/"+sid+"/file-at?path=note.txt&sha="+oldest, nil)
	if code != 200 || !strings.Contains(string(body), "version one") {
		t.Fatalf("file-at %d: %s", code, body)
	}

	// restore to oldest
	e.mustJSON(t, "POST", "/api/sessions/"+sid+"/restore-file",
		map[string]any{"path": "note.txt", "sha": oldest}, nil)
	_, body = e.do(t, "GET", "/api/sessions/"+sid+"/file?path=note.txt", nil)
	if !strings.Contains(string(body), "version one") {
		t.Fatalf("after restore=%s", body)
	}

	// user edit behind our back → 409 without force
	os.WriteFile(filepath.Join(e.wsdir, "note.txt"), []byte("user typed this"), 0o644)
	var hist2 []map[string]any
	e.mustJSON(t, "GET", "/api/sessions/"+sid+"/file-history?path=note.txt", nil, &hist2)
	code, body = e.do(t, "POST", "/api/sessions/"+sid+"/restore-file",
		map[string]any{"path": "note.txt", "sha": hist2[0]["sha"].(string)})
	if code != 409 {
		t.Fatalf("restore over user edits → %d want 409: %s", code, body)
	}
	e.mustJSON(t, "POST", "/api/sessions/"+sid+"/restore-file",
		map[string]any{"path": "note.txt", "sha": hist2[0]["sha"].(string), "force": true}, nil)
}

// --- P6-6: genisys_tool over the wire -----------------------------------------

func TestGenisysTool(t *testing.T) {
	e := newEnv(t, []string{
		sseToolCall("g1", "genisys_tool", `{"action":"session_info"}`),
		sseToolCall("g2", "genisys_tool", `{"action":"config_get"}`),
		sseText("all good"),
	}, 0)
	sid := e.newSession(t)
	e.mustJSON(t, "POST", "/api/sessions/"+sid+"/prompt", map[string]any{"text": "tell me about yourself"}, nil)
	e.waitIdle(t, sid)

	_, msgs := e.do(t, "GET", "/api/sessions/"+sid+"/messages", nil)
	body := string(msgs)
	if !strings.Contains(body, "model_ref") {
		t.Fatalf("session_info result missing from transcript: %s", body)
	}
	if !strings.Contains(body, "agent:") && !strings.Contains(body, "endpoints") {
		t.Fatalf("config_get result missing: %s", body)
	}
	// the tool result must be persisted as a tool message (introspection
	// survives resume)
	var ms []map[string]any
	e.mustJSON(t, "GET", "/api/sessions/"+sid+"/messages", nil, &ms)
	var toolMsgs int
	for _, m := range ms {
		if m["role"] == "tool" {
			toolMsgs++
		}
	}
	if toolMsgs < 2 {
		t.Fatalf("tool messages=%d want >=2", toolMsgs)
	}
}

// --- P6-4: settings screens ↔ config.yaml -------------------------------------

func TestSettingsRoundTrip(t *testing.T) {
	e := newEnv(t, []string{sseText("ok")}, 0)
	seed := `# genisys config
version: 1
default_model: mock/m1
endpoints:
  - id: mock
    base_url: http://127.0.0.1:1/v1 # endpoint comment
    models:
      - id: m1
browser:
  allowed_origins:
    - http://localhost:3000 # origin comment
web:
  provider: searxng
  searxng:
    base_url: http://sx.example # instance comment
`
	if err := os.WriteFile(e.app.CfgPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.app.ReloadeCfg(); err != nil {
		t.Fatal(err)
	}

	var st map[string]any
	e.mustJSON(t, "GET", "/api/settings", nil, &st)
	if st["default_model"] != "mock/m1" {
		t.Fatalf("default_model=%v", st["default_model"])
	}
	br := st["browser"].(map[string]any)
	if o := br["allowed_origins"].([]any); len(o) != 1 || o[0] != "http://localhost:3000" {
		t.Fatalf("origins=%v", br["allowed_origins"])
	}

	// scalar PATCH keeps comments
	e.mustJSON(t, "PATCH", "/api/config", map[string]string{"web.searxng.base_url": "http://sx2.example"}, nil)
	b, _ := os.ReadFile(e.app.CfgPath)
	s := string(b)
	if !strings.Contains(s, "http://sx2.example") || !strings.Contains(s, "# instance comment") {
		t.Fatalf("comment not preserved:\n%s", s)
	}

	// list PATCH stays a sequence
	e.mustJSON(t, "PATCH", "/api/config", map[string]string{"browser.allowed_origins": "https://a.com, https://b.com"}, nil)
	e.mustJSON(t, "GET", "/api/settings", nil, &st)
	if o := st["browser"].(map[string]any)["allowed_origins"].([]any); len(o) != 2 {
		t.Fatalf("origins after patch=%v", o)
	}
	b, _ = os.ReadFile(e.app.CfgPath)
	if !strings.Contains(string(b), "- https://a.com") {
		t.Fatalf("list not written as sequence:\n%s", b)
	}

	// PUT validates before writing
	code, _ := e.do(t, "PUT", "/api/config", map[string]string{"yaml": "version: [broken"})
	if code != 400 {
		t.Fatalf("bad yaml → %d", code)
	}
	b, _ = os.ReadFile(e.app.CfgPath)
	if !strings.Contains(string(b), "# genisys config") {
		t.Fatal("rejected PUT must not touch the file")
	}

	// web probe answers
	code, _ = e.do(t, "POST", "/api/web/probe", nil)
	if code != 200 {
		t.Fatalf("web probe → %d", code)
	}
}

// P6-1 DoD: "fork mid-run branches cleanly".
func TestForkMidRun(t *testing.T) {
	e := newEnv(t, []string{sseText("original answer"), sseText("fork answer")}, 300*time.Millisecond)
	sid := e.newSession(t)
	e.mustJSON(t, "POST", "/api/sessions/"+sid+"/prompt", map[string]any{"text": "slow one"}, nil)

	// fork once the user turn is persisted (fork snapshots persisted
	// state; the prompt POST returns before the worker writes it)
	deadline := time.Now().Add(10 * time.Second)
	for {
		var ms []map[string]any
		e.mustJSON(t, "GET", "/api/sessions/"+sid+"/messages", nil, &ms)
		var users int
		for _, m := range ms {
			if m["role"] == "user" {
				users++
			}
		}
		if users >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	// while still running: fork
	var forked struct {
		ID string `json:"id"`
	}
	e.mustJSON(t, "POST", "/api/sessions/"+sid+"/fork", nil, &forked)
	if forked.ID == "" || forked.ID == sid {
		t.Fatalf("fork id %q", forked.ID)
	}
	var orig struct {
		Live bool `json:"live"`
	}
	e.mustJSON(t, "GET", "/api/sessions/"+sid, nil, &orig)
	if !orig.Live {
		t.Fatal("original should still be live")
	}

	e.waitIdle(t, sid)

	// fork carries the user turn but not the in-flight answer
	var msgs []map[string]any
	e.mustJSON(t, "GET", "/api/sessions/"+forked.ID+"/messages", nil, &msgs)
	var userTurns, answers int
	for _, m := range msgs {
		switch m["role"] {
		case "user":
			userTurns++
		case "assistant":
			answers++
		}
	}
	if userTurns != 1 {
		t.Fatalf("fork user turns=%d want 1", userTurns)
	}
	if answers != 0 {
		t.Fatalf("fork must not carry the in-flight answer, got %d", answers)
	}

	// both sessions run independently afterwards
	e.mustJSON(t, "POST", "/api/sessions/"+forked.ID+"/prompt", map[string]any{"text": "fork branch"}, nil)
	e.waitIdle(t, forked.ID)
	e.mustJSON(t, "GET", "/api/sessions/"+sid+"/messages", nil, &msgs)
}
