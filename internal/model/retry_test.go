package model

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func sseHandler(body string, status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if status != 0 && status != 200 {
			w.WriteHeader(status)
			io.WriteString(w, "nope")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, body)
	}
}

const textOnce = `data: {"choices":[{"delta":{"content":"ok"}}]}

data: [DONE]

`

func TestCompleteRetriesTransient(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n <= 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			io.WriteString(w, "slow down")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, textOnce)
	}))
	defer srv.Close()

	c, _ := New(srv.URL, "")
	comp, err := c.CompleteWith(context.Background(), ChatRequest{Model: "m"},
		&RetryPolicy{MaxAttempts: 4, Base: time.Millisecond, MaxSleep: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if comp.Text != "ok" || comp.Attempts != 3 {
		t.Errorf("comp = %+v", comp)
	}
	if hits.Load() != 3 {
		t.Errorf("attempts = %d, want 3", hits.Load())
	}
}

func TestCompleteGivesUpAfterMaxAttempts(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		sseHandler("", 500)(w, r)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "")
	_, err := c.CompleteWith(context.Background(), ChatRequest{Model: "m"},
		&RetryPolicy{MaxAttempts: 2, Base: time.Millisecond, MaxSleep: 2 * time.Millisecond})
	if err == nil {
		t.Fatal("want error")
	}
	if hits.Load() != 2 {
		t.Errorf("attempts = %d, want 2", hits.Load())
	}
}

func TestCompleteNoRetryOnAuth(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		sseHandler("", 401)(w, r)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "")
	if _, err := c.Complete(context.Background(), ChatRequest{Model: "m"}); err == nil {
		t.Fatal("want error")
	} else if ae, ok := AsAPIError(err); !ok || ae.Kind != KindAuth {
		t.Errorf("err = %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("auth must not retry, hits = %d", hits.Load())
	}
}

// SPEC §13.3: mid-stream drop WITH complete tool calls → accept; prose-only → retry.
func TestMidStreamDropPolicy(t *testing.T) {
	toolDrop := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"tc_1","type":"function","function":{"name":"list_dir","arguments":"{\"path\": \".\"}"}}]}}]}

` // server closes here, no [DONE]
	proseDrop := `data: {"choices":[{"delta":{"content":"half a thought"}}]}

`
	t.Run("accept when tool calls complete", func(t *testing.T) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, toolDrop)
		}))
		defer srv.Close()
		c, _ := New(srv.URL, "")
		comp, err := c.Complete(context.Background(), ChatRequest{Model: "m"})
		if err != nil {
			t.Fatalf("should accept tool-call drop: %v", err)
		}
		if len(comp.ToolCalls) != 1 || comp.ToolCalls[0].Function.Name != "list_dir" {
			t.Errorf("tool calls: %+v", comp.ToolCalls)
		}
		if hits.Load() != 1 {
			t.Errorf("must not retry, hits=%d", hits.Load())
		}
	})

	t.Run("retry prose-only drop then succeed", func(t *testing.T) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := hits.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			if n == 1 {
				io.WriteString(w, proseDrop) // drop
				return
			}
			io.WriteString(w, textOnce)
		}))
		defer srv.Close()
		c, _ := New(srv.URL, "")
		comp, err := c.CompleteWith(context.Background(), ChatRequest{Model: "m"},
			&RetryPolicy{MaxAttempts: 3, Base: time.Millisecond, MaxSleep: 2 * time.Millisecond})
		if err != nil {
			t.Fatalf("retry should recover: %v", err)
		}
		if comp.Text != "ok" || hits.Load() != 2 {
			t.Errorf("comp=%+q hits=%d", comp.Text, hits.Load())
		}
	})
}

func TestCompleteCancelDuringStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := c.Complete(ctx, ChatRequest{Model: "m"})
	if err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestToolCallAssemblySynthesizesIDs(t *testing.T) {
	body := `data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"name":"b","arguments":"{}"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"a","arguments":"{}"}}]}}]}

data: [DONE]

`
	srv := httptest.NewServer(sseHandler(body, 0))
	defer srv.Close()
	c, _ := New(srv.URL, "")
	comp, err := c.Complete(context.Background(), ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(comp.ToolCalls) != 2 {
		t.Fatalf("calls = %d", len(comp.ToolCalls))
	}
	// ordered by index: index 0 first, synthesized ids
	if comp.ToolCalls[0].Function.Name != "a" || comp.ToolCalls[0].ID != "tc_1" {
		t.Errorf("first = %+v", comp.ToolCalls[0])
	}
	if comp.ToolCalls[1].Function.Name != "b" || comp.ToolCalls[1].ID != "tc_2" {
		t.Errorf("second = %+v", comp.ToolCalls[1])
	}
}
