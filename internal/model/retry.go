package model

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"slices"
	"strings"
	"time"
)

// RetryPolicy implements SPEC §13.3: exponential backoff on 429/5xx/transport
// errors, honoring Retry-After.
type RetryPolicy struct {
	MaxAttempts int           // default 4
	Base        time.Duration // first backoff, default 1s (then 2s, 4s, 8s)
	MaxSleep    time.Duration // cap, default 30s
}

// DefaultRetryPolicy is the SPEC §13.3 default: 1s→2s→4s→8s, max 4 attempts.
func DefaultRetryPolicy() *RetryPolicy {
	return &RetryPolicy{MaxAttempts: 4, Base: time.Second, MaxSleep: 30 * time.Second}
}

func (p *RetryPolicy) fill() *RetryPolicy {
	q := DefaultRetryPolicy()
	if p == nil {
		return q
	}
	if p.MaxAttempts > 0 {
		q.MaxAttempts = p.MaxAttempts
	}
	if p.Base > 0 {
		q.Base = p.Base
	}
	if p.MaxSleep > 0 {
		q.MaxSleep = p.MaxSleep
	}
	return q
}

// sleep waits d (capped) or until ctx is done.
func (p *RetryPolicy) wait(ctx context.Context, attempt int, retryAfter time.Duration) error {
	d := retryAfter
	if d <= 0 {
		d = p.Base << (attempt - 1)
		if d > p.MaxSleep {
			d = p.MaxSleep
		}
		d += time.Duration(rand.Int64N(int64(p.Base))) // jitter
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Completion is a fully buffered completion (the shape the P1 loop consumes;
// P3 will stream instead and apply retry at the loop level).
type Completion struct {
	Text         string
	Reasoning    string
	ToolCalls    []ToolCall // assembled, ordered by index; ids synthesized when missing
	Usage        Usage
	FinishReason string
	Attempts     int // how many HTTP attempts were needed
}

// HasToolCalls reports whether the completion requests tool execution.
func (c *Completion) HasToolCalls() bool { return len(c.ToolCalls) > 0 }

// Complete runs chat/completions to completion with the SPEC §13.3 retry
// policy. Mid-stream drops: a drop that already yielded complete tool calls
// is ACCEPTED (tool args are self-terminating JSON); a drop that yielded only
// prose is retried.
func (c *Client) Complete(ctx context.Context, req ChatRequest) (*Completion, error) {
	return c.CompleteWith(ctx, req, nil)
}

// CompleteWith is Complete with a custom retry policy.
func (c *Client) CompleteWith(ctx context.Context, req ChatRequest, p *RetryPolicy) (*Completion, error) {
	pol := p.fill()
	var lastErr error
	for attempt := 1; attempt <= pol.MaxAttempts; attempt++ {
		comp, err := c.completeOnce(ctx, req)
		switch {
		case err == nil:
			comp.Attempts = attempt
			return comp, nil
		case ctx.Err() != nil:
			return comp, ctx.Err() // interrupt wins (SPEC §7.4)
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return comp, err
		}
		lastErr = err
		// Mid-stream drop with complete tool calls → accept (SPEC §13.3).
		if comp != nil && comp.HasToolCalls() && isStreamDrop(err) {
			comp.Attempts = attempt
			return comp, nil
		}
		if !IsTransient(err) {
			return nil, err
		}
		if attempt == pol.MaxAttempts {
			break
		}
		ra := time.Duration(0)
		if ae, ok := AsAPIError(err); ok {
			ra = ae.RetryAfter
		}
		if werr := pol.wait(ctx, attempt, ra); werr != nil {
			return nil, werr
		}
	}
	return nil, lastErr
}

// completeOnce performs a single attempt and buffers the stream.
// On error it returns the partial Completion (so the drop policy can judge).
func (c *Client) completeOnce(ctx context.Context, req ChatRequest) (*Completion, error) {
	h, err := c.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	comp := &Completion{}
	acc := newToolCallAccumulator()
	for ev := range h.Events {
		switch ev.Kind {
		case KindTextDelta:
			comp.Text += ev.Text
		case KindReasoningDelta:
			comp.Reasoning += ev.Text
		case KindToolCallDelta:
			acc.add(ev.ToolCall)
		case KindUsage:
			comp.Usage = *ev.Usage
		}
	}
	comp.ToolCalls = acc.done()
	if c.quirks[QuirkEmitsThinkTags] {
		comp.Text = StripThinkTags(comp.Text)
	}
	if err := h.Err(); err != nil {
		return comp, err
	}
	return comp, nil
}

// isStreamDrop reports an unexpected end of stream (server closed the SSE
// body before [DONE]).
func isStreamDrop(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	if ae, ok := AsAPIError(err); ok && ae.Kind == KindStream {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "connection reset") || strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "unexpected EOF") || strings.Contains(msg, "EOF")
}

// --- tool call assembly by index (shared by Complete and the P1-4 parser) --

type tacc struct {
	order []int
	byIdx map[int]*ToolCall
}

func newToolCallAccumulator() *tacc {
	return &tacc{byIdx: map[int]*ToolCall{}}
}

func (a *tacc) add(d *ToolCallDelta) {
	tc, ok := a.byIdx[d.Index]
	if !ok {
		tc = &ToolCall{Type: "function"}
		a.byIdx[d.Index] = tc
		a.order = append(a.order, d.Index)
	}
	if d.ID != "" {
		tc.ID = d.ID
	}
	if d.Type != "" {
		tc.Type = d.Type
	}
	if d.Name != "" {
		tc.Function.Name = d.Name
	}
	tc.Function.Arguments += d.Arguments
}

// done returns calls ordered by index; missing ids are synthesized tc_<n>
// (SPEC §13.4: transcripts stay OpenAI-shaped and portable).
func (a *tacc) done() []ToolCall {
	sorted := make([]int, len(a.order))
	copy(sorted, a.order)
	slices.Sort(sorted)
	out := make([]ToolCall, 0, len(sorted))
	for i, idx := range sorted {
		tc := *a.byIdx[idx]
		if tc.ID == "" {
			tc.ID = SynthesizeID(i)
		}
		if tc.Type == "" {
			tc.Type = "function"
		}
		out = append(out, tc)
	}
	return out
}

// SynthesizeID builds the fallback tool-call id format (SPEC §13.4).
func SynthesizeID(n int) string {
	return "tc_" + itoa(n+1)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
