package model

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// goldenServer replays a recorded SSE fixture (P1-1 DoD: golden-file replay).
func goldenServer(t *testing.T, fixture string) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile("../../testdata/golden/sse/" + fixture)
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %q (base_url normalization broken?)", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write(body)
	}))
}

func collect(t *testing.T, h *StreamHandle) ([]StreamEvent, error) {
	t.Helper()
	var evs []StreamEvent
	for ev := range h.Events {
		evs = append(evs, ev)
	}
	return evs, h.Err()
}

func TestStreamTextGolden(t *testing.T) {
	srv := goldenServer(t, "text.sse")
	defer srv.Close()

	c, err := New(srv.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Stream(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	evs, err := collect(t, h)
	if err != nil {
		t.Fatalf("stream err: %v", err)
	}
	var text strings.Builder
	var usage *Usage
	var done bool
	for _, ev := range evs {
		switch ev.Kind {
		case KindTextDelta:
			text.WriteString(ev.Text)
		case KindUsage:
			usage = ev.Usage
		case KindDone:
			done = true
		}
	}
	if text.String() != "Hello from vLLM!" {
		t.Errorf("assembled text = %q", text.String())
	}
	if usage == nil || usage.PromptTokens != 11 || usage.CompletionTokens != 4 || usage.TotalTokens != 15 {
		t.Errorf("usage not captured: %+v", usage)
	}
	if !done {
		t.Error("missing terminal Done event")
	}
}

func TestStreamToolCallAssembly(t *testing.T) {
	srv := goldenServer(t, "toolcall.sse")
	defer srv.Close()
	c, _ := New(srv.URL, "")
	h, err := c.Stream(context.Background(), ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	evs, err := collect(t, h)
	if err != nil {
		t.Fatal(err)
	}
	// reassemble by index (what P1-4's native parser will consume)
	type acc struct{ id, name, args string }
	accs := map[int]*acc{}
	for _, ev := range evs {
		if ev.Kind != KindToolCallDelta {
			continue
		}
		tc := ev.ToolCall
		a := accs[tc.Index]
		if a == nil {
			a = &acc{}
			accs[tc.Index] = a
		}
		if tc.ID != "" {
			a.id = tc.ID
		}
		if tc.Name != "" {
			a.name = tc.Name
		}
		a.args += tc.Arguments
	}
	a := accs[0]
	if a == nil {
		t.Fatal("no tool call deltas")
	}
	if a.id != "tc_1" || a.name != "read_file_lines" {
		t.Errorf("id/name = %q/%q", a.id, a.name)
	}
	if a.args != `{"path": "main.go"}` {
		t.Errorf("assembled args = %q", a.args)
	}
}

func TestStreamCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done() // hang until client goes away
	}))
	defer srv.Close()

	c, _ := New(srv.URL, "")
	ctx, cancel := context.WithCancel(context.Background())
	h, err := c.Stream(ctx, ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	got := <-h.Events // first delta
	if got.Kind != KindTextDelta || got.Text != "partial" {
		t.Fatalf("first event: %+v", got)
	}
	cancel() // SPEC §7.4 interrupt: abort the SSE read
	_, err = collect(t, h)
	if err != context.Canceled {
		t.Errorf("Err() = %v, want context.Canceled", err)
	}
}

func TestErrorTaxonomy(t *testing.T) {
	cases := []struct {
		status     int
		retryAfter string
		wantKind   string
		transient  bool
	}{
		{401, "", KindAuth, false},
		{403, "", KindAuth, false},
		{404, "", KindNotFound, false},
		{400, "", KindRequest, false},
		{429, "3", KindRateLimit, true},
		{500, "", KindServer, true},
		{503, "", KindServer, true},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tc.retryAfter != "" {
				w.Header().Set("Retry-After", tc.retryAfter)
			}
			w.WriteHeader(tc.status)
			io.WriteString(w, "server says no")
		}))
		c, _ := New(srv.URL, "k")
		_, err := c.Stream(context.Background(), ChatRequest{Model: "m"})
		srv.Close()
		ae, ok := AsAPIError(err)
		if !ok {
			t.Fatalf("status %d: not an APIError: %v", tc.status, err)
		}
		if ae.Kind != tc.wantKind {
			t.Errorf("status %d: kind %q, want %q", tc.status, ae.Kind, tc.wantKind)
		}
		if tc.status == 429 && ae.RetryAfter != 3*time.Second {
			t.Errorf("Retry-After parse: %v", ae.RetryAfter)
		}
		if IsTransient(err) != tc.transient {
			t.Errorf("status %d: IsTransient = %v", tc.status, tc.transient)
		}
	}
}

func TestNormalizeBase(t *testing.T) {
	cases := map[string]string{
		"http://h:8000":              "http://h:8000/v1",
		"http://h:8000/":             "http://h:8000/v1",
		"http://h:8000/v1":           "http://h:8000/v1",
		"https://api.openai.com/v1/": "https://api.openai.com/v1",
		"http://h/v1/extra":          "http://h/v1/extra/v1", // explicit suffix only
	}
	for in, want := range cases {
		got, err := NormalizeBase(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizeBase(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "  ", "h://x", "ftp://x"} {
		if _, err := NormalizeBase(bad); err == nil {
			t.Errorf("NormalizeBase(%q): expected error", bad)
		}
	}
}

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path %q", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer secret" {
			t.Errorf("auth header %q", auth)
		}
		io.WriteString(w, `{"data":[{"id":"a"},{"id":"b"}]}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "secret")
	ids, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Errorf("ids = %v", ids)
	}
}
