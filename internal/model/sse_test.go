package model

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseSSEBasics(t *testing.T) {
	in := strings.Join([]string{
		": keep-alive comment",
		"event: message",
		"id: 42",
		"data: line one",
		"data: line two",
		"",
		"data: [DONE]",
		"",
	}, "\r\n")
	var got []SSEEvent
	if err := ParseSSE(strings.NewReader(in), func(e SSEEvent) error {
		got = append(got, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("events = %d, want 2: %+v", len(got), got)
	}
	if got[0].Data != "line one\nline two" {
		t.Errorf("multi-line data join: %q", got[0].Data)
	}
	if got[0].ID != "42" || got[0].Event != "message" {
		t.Errorf("id/event: %+v", got[0])
	}
	if got[1].Data != "[DONE]" {
		t.Errorf("done record: %q", got[1].Data)
	}
}

func TestParseSSEFlushesAtEOF(t *testing.T) {
	// server closed without trailing blank line
	var got []SSEEvent
	err := ParseSSE(strings.NewReader("data: last\n"), func(e SSEEvent) error {
		got = append(got, e)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Data != "last" {
		t.Fatalf("EOF flush: %+v", got)
	}
}

func TestParseSSECallbackErrorPropagates(t *testing.T) {
	sentinel := errors.New("stop")
	err := ParseSSE(strings.NewReader("data: x\n\n"), func(SSEEvent) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseSSERetryField(t *testing.T) {
	var got SSEEvent
	_ = ParseSSE(strings.NewReader("retry: 1500\ndata: x\n\n"), func(e SSEEvent) error {
		got = e
		return nil
	})
	if got.Retry == nil || *got.Retry != 1500*time.Millisecond {
		t.Errorf("retry: %v", got.Retry)
	}
}
