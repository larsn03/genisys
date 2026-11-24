package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client talks to one OpenAI-compatible endpoint (SPEC §13.1). Safe for
// concurrent use. Zero-value knobs get sane defaults.
type Client struct {
	baseURL string
	apiKey  string
	hc      *http.Client

	// includeUsage sends stream_options.include_usage; probed per endpoint
	// in P1-3, degrades silently (SPEC §13.1).
	includeUsage bool
}

// Option customizes a Client.
type Option func(*Client)

// WithHTTPClient overrides the transport (timeouts, proxies, tests).
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// WithIncludeUsage toggles stream_options.include_usage.
func WithIncludeUsage(on bool) Option { return func(c *Client) { c.includeUsage = on } }

// New builds a client for base_url (… may or may not end in /v1 — normalized).
func New(baseURL, apiKey string, opts ...Option) (*Client, error) {
	u, err := NormalizeBase(baseURL)
	if err != nil {
		return nil, err
	}
	c := &Client{baseURL: u, apiKey: apiKey, hc: &http.Client{}, includeUsage: true}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// NormalizeBase trims trailing slashes and ensures a /v1 suffix (SPEC §13.1:
// "base_url may or may not end in /v1 — normalize both").
func NormalizeBase(baseURL string) (string, error) {
	u := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if u == "" {
		return "", fmt.Errorf("model: empty base_url")
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return "", fmt.Errorf("model: base_url must be http(s), got %q", baseURL)
	}
	if !strings.HasSuffix(u, "/v1") {
		u += "/v1"
	}
	return u, nil
}

// StreamHandle is the live completion stream (IMPL §4). Cancel the request
// context to abort mid-stream (SPEC §7.4 interrupt); Events closes when the
// stream ends, after which Err() reports the terminal error (nil = clean end).
type StreamHandle struct {
	Events <-chan StreamEvent

	mu     sync.Mutex
	err    error
	closed chan struct{}
}

// Err blocks until the stream is finished and returns its terminal error.
func (h *StreamHandle) Err() error {
	<-h.closed
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

func (h *StreamHandle) setErr(err error) {
	h.mu.Lock()
	if h.err == nil && err != nil {
		h.err = err
	}
	h.mu.Unlock()
}

// Stream sends req with stream:true and pumps normalized events. The returned
// handle's Events channel is buffered and closed on completion/error.
func (c *Client) Stream(ctx context.Context, req ChatRequest) (*StreamHandle, error) {
	req.Stream = true
	if c.includeUsage {
		req.StreamOptions = &StreamOptions{IncludeUsage: true}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("model: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return nil, err // transport error: retryable by P1-2 policy
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, NewAPIError(resp.StatusCode, strings.TrimSpace(string(snippet)), retryAfter(resp))
	}

	h := &StreamHandle{closed: make(chan struct{})}
	events := make(chan StreamEvent, 64)
	h.Events = events
	go c.pump(ctx, resp.Body, events, h)
	return h, nil
}

func (c *Client) pump(ctx context.Context, body io.ReadCloser, events chan<- StreamEvent, h *StreamHandle) {
	defer body.Close()
	defer close(events)
	defer close(h.closed)

	send := func(ev StreamEvent) error {
		select {
		case events <- ev:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	err := ParseSSE(body, func(rec SSEEvent) error {
		if rec.Data == "[DONE]" {
			return io.EOF // stop parsing; clean end below
		}
		var chunk chatChunk
		if uerr := json.Unmarshal([]byte(rec.Data), &chunk); uerr != nil {
			h.setErr(fmt.Errorf("%w: malformed SSE chunk: %v", errStream, uerr))
			return io.ErrUnexpectedEOF
		}
		if chunk.Usage != nil {
			if serr := send(StreamEvent{Kind: KindUsage, Usage: chunk.Usage}); serr != nil {
				return serr
			}
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.ReasoningContent != "" {
				if serr := send(StreamEvent{Kind: KindReasoningDelta, Text: ch.Delta.ReasoningContent}); serr != nil {
					return serr
				}
			}
			if ch.Delta.Content != "" {
				if serr := send(StreamEvent{Kind: KindTextDelta, Text: ch.Delta.Content}); serr != nil {
					return serr
				}
			}
			if len(ch.Delta.ToolCalls) > 0 {
				tcs, terr := ch.Delta.toolCallDeltas()
				if terr != nil {
					h.setErr(fmt.Errorf("%w: %v", errStream, terr))
					return io.ErrUnexpectedEOF
				}
				for i := range tcs {
					tc := tcs[i]
					if serr := send(StreamEvent{Kind: KindToolCallDelta, ToolCall: &tc}); serr != nil {
						return serr
					}
				}
			}
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				// Keep reading: usage often arrives in a final chunk AFTER
				// finish_reason (vLLM, llama.cpp); [DONE] or EOF ends the stream.
				continue
			}
		}
		return nil
	})
	switch {
	case err == nil || err == io.EOF:
		// clean end
	default:
		if ctxErr := ctx.Err(); ctxErr != nil {
			h.setErr(ctxErr) // interrupt wins over the resulting read error
		} else {
			h.setErr(err)
		}
	}
	// Always surface a terminal Done event (unless cancelled) so the loop
	// has one unambiguous stop signal regardless of how the stream ended.
	if ctx.Err() == nil {
		send(StreamEvent{Kind: KindDone})
	}
}

var errStream = &APIError{Kind: KindStream, Message: "stream error"}

// ListModels probes GET /v1/models for the model picker (SPEC §13.1).
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, NewAPIError(resp.StatusCode, strings.TrimSpace(string(snippet)), retryAfter(resp))
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("models: %w", err)
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

func retryAfter(resp *http.Response) time.Duration {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
