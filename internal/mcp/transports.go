// transports.go — stdio (newline-delimited JSON-RPC over an exec'd
// command) and HTTP (JSON-RPC over POST, JSON or SSE replies).

package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// --- stdio ------------------------------------------------------------------

type stdioTransport struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	mu         sync.Mutex
	pending    map[int64]chan rpcMsg
	readerDone chan struct{}
}

func newStdio(ctx context.Context, cfg ServerConfig) (transport, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("mcp: %s: no command", cfg.Name)
	}
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %s: %w", cfg.Name, err)
	}
	t := &stdioTransport{
		cmd: cmd, stdin: stdin,
		pending: map[int64]chan rpcMsg{}, readerDone: make(chan struct{}),
	}
	go t.readLoop(bufio.NewScanner(stdout))
	return t, nil
}

func (t *stdioTransport) readLoop(sc *bufio.Scanner) {
	defer close(t.readerDone)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var m rpcMsg
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		if m.ID == 0 {
			continue // notifications/requests to us: ignored
		}
		t.mu.Lock()
		ch := t.pending[m.ID]
		delete(t.pending, m.ID)
		t.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
	// EOF: fail all pending
	t.mu.Lock()
	for id, ch := range t.pending {
		close(ch)
		delete(t.pending, id)
	}
	t.mu.Unlock()
}

func (t *stdioTransport) send(ctx context.Context, m rpcMsg) (rpcMsg, error) {
	ch := make(chan rpcMsg, 1)
	t.mu.Lock()
	t.pending[m.ID] = ch
	t.mu.Unlock()
	if err := encode(t.stdin, m); err != nil {
		return rpcMsg{}, err
	}
	select {
	case resp, ok := <-ch:
		if !ok {
			return rpcMsg{}, fmt.Errorf("%w: connection closed", ErrNotConnected)
		}
		return resp, nil
	case <-ctx.Done():
		return rpcMsg{}, ctx.Err()
	}
}

func (t *stdioTransport) notify(ctx context.Context, m rpcMsg) error {
	return encode(t.stdin, m)
}

func (t *stdioTransport) close() error {
	_ = t.stdin.Close()
	done := make(chan struct{})
	go func() { _ = t.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = t.cmd.Process.Kill()
	}
	return nil
}

// --- http -------------------------------------------------------------------

type httpTransport struct {
	url string
	hc  *http.Client
	mu  sync.Mutex
}

func newHTTPTransport(cfg ServerConfig) transport {
	return &httpTransport{url: cfg.URL, hc: &http.Client{Timeout: 60 * time.Second}}
}

func (t *httpTransport) post(ctx context.Context, m rpcMsg) (rpcMsg, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return rpcMsg{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(b))
	if err != nil {
		return rpcMsg{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := t.hc.Do(req)
	if err != nil {
		return rpcMsg{}, fmt.Errorf("%w: %v", ErrNotConnected, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return rpcMsg{}, err
	}
	if resp.StatusCode >= 400 {
		return rpcMsg{}, fmt.Errorf("%w: HTTP %d", ErrNotConnected, resp.StatusCode)
	}
	// notifications: 202/204 with no body
	if m.ID == 0 {
		return rpcMsg{}, nil
	}
	// plain JSON
	var out rpcMsg
	if json.Valid(body) {
		if err := json.Unmarshal(body, &out); err != nil {
			return rpcMsg{}, err
		}
		return out, nil
	}
	// SSE: take the first data: line carrying a response with our id
	for _, line := range strings.Split(string(body), "\n") {
		data, ok := strings.CutPrefix(strings.TrimSpace(line), "data:")
		if !ok {
			continue
		}
		var msg rpcMsg
		if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &msg); err != nil {
			continue
		}
		if msg.ID == m.ID {
			return msg, nil
		}
	}
	return rpcMsg{}, fmt.Errorf("%w: no matching response in payload", ErrNotConnected)
}

func (t *httpTransport) send(ctx context.Context, m rpcMsg) (rpcMsg, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.post(ctx, m)
}

func (t *httpTransport) notify(ctx context.Context, m rpcMsg) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := t.post(ctx, m)
	return err
}

func (t *httpTransport) close() error { return nil }
