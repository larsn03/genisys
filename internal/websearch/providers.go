// tavily.go + brave.go — the optional commercial providers (SPEC §16).
// Same contract; opt-in via web.provider; no silent fallback.

package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

const userAgent = "genisys/0.1 (+https://github.com/larsn03/genisys)"

type keyed struct {
	hc    *http.Client
	token string
	base  string
	name  string
	self  Provider // set by newTavily/newBrave for Probe
}

func newHTTP(cfg Config, defBase, name string) (*keyed, error) {
	base := cfg.BaseURL
	if base == "" {
		base = defBase
	}
	if _, err := url.Parse(base); err != nil {
		return nil, fmt.Errorf("websearch: bad base_url: %w", err)
	}
	k := &keyed{
		hc:   &http.Client{Timeout: time.Duration(cfg.TimeoutMs) * time.Millisecond},
		base: base, name: name,
	}
	_ = k
	if cfg.AllowInsecureTLS {
		k.hc.Transport = newInsecureTransport()
	}
	if cfg.APIKeyEnv != "" {
		k.token = os.Getenv(cfg.APIKeyEnv)
	}
	if k.token == "" {
		return nil, fmt.Errorf("websearch: %s: no API token (set the env var named by web.%s.api_key_env)", name, name)
	}
	return k, nil
}

func (k *keyed) Name() string { return k.name }

func (k *keyed) do(ctx context.Context, method, u string, body []byte, hdr map[string]string) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	for kk, vv := range hdr {
		req.Header.Set(kk, vv)
	}
	resp, err := k.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: %s rejected the API token", ErrAuthRequired, k.name)
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: %s status %d", ErrUnreachable, k.name, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: unexpected status %d", k.name, resp.StatusCode)
	}
	return b, nil
}

func (k *keyed) Probe(ctx context.Context) string {
	_, err := k.self.SearchOne(ctx, "genisys")
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrAuthRequired):
		return "auth-required"
	case errors.Is(err, ErrUnreachable):
		return "unreachable"
	default:
		return "not-searxng" // provider answered but payload was unexpected
	}
}

// --- tavily ---------------------------------------------------------------

type tavily struct{ *keyed }

func newTavily(cfg Config) (Provider, error) {
	k, err := newHTTP(cfg, "https://api.tavily.com", "tavily")
	if err != nil {
		return nil, err
	}
	t := &tavily{k}
	k.self = t
	return t, nil
}

func (t *tavily) SearchOne(ctx context.Context, q string) ([]Result, error) {
	body, _ := json.Marshal(map[string]any{"query": q, "max_results": 8})
	b, err := t.do(ctx, http.MethodPost, t.base+"/search", body,
		map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + t.token})
	if err != nil {
		return nil, err
	}
	var payload struct {
		Results []struct {
			Title      string `json:"title"`
			URL        string `json:"url"`
			Content    string `json:"content"`
			RawContent string `json:"raw_content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &payload); err != nil {
		return nil, fmt.Errorf("tavily: unexpected payload: %w", err)
	}
	out := make([]Result, 0, len(payload.Results))
	for _, r := range payload.Results {
		if r.URL == "" {
			continue
		}
		out = append(out, Result{Title: r.Title, URL: r.URL, Content: clip(r.Content, 400)})
	}
	return out, nil
}

// --- brave ----------------------------------------------------------------

type brave struct{ *keyed }

func newBrave(cfg Config) (Provider, error) {
	k, err := newHTTP(cfg, "https://api.search.brave.com", "brave")
	if err != nil {
		return nil, err
	}
	b := &brave{k}
	k.self = b
	return b, nil
}

func (b0 *brave) SearchOne(ctx context.Context, q string) ([]Result, error) {
	u := b0.base + "/res/v1/web/search?" + url.Values{"q": {q}, "count": {"8"}}.Encode()
	b, err := b0.do(ctx, http.MethodGet, u, nil,
		map[string]string{"X-Subscription-Token": b0.token})
	if err != nil {
		return nil, err
	}
	var payload struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(b, &payload); err != nil {
		return nil, fmt.Errorf("brave: unexpected payload: %w", err)
	}
	out := make([]Result, 0, len(payload.Web.Results))
	for _, r := range payload.Web.Results {
		if r.URL == "" {
			continue
		}
		out = append(out, Result{Title: r.Title, URL: r.URL, Content: clip(r.Description, 400)})
	}
	return out, nil
}
