// Package websearch implements the SearXNG backend of web_search
// (SPEC §9.5, normative): any reachable instance, local or remote, over
// the JSON API. No third-party fallback — queries go only to the
// configured instance.
package websearch

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Sentinel classifications (SPEC §9.5 reachability check).
var (
	ErrJSONDisabled = errors.New("searxng: JSON output disabled on the instance — add \"- json\" to search.formats in settings.yml and restart the instance (SPEC §9.5)")
	ErrAuthRequired = errors.New("searxng: instance requires authentication — set the token env var named by web.searxng.api_key_env")
	ErrUnreachable  = errors.New("searxng: instance unreachable")
	ErrNotSearxng   = errors.New("searxng: response is not a SearXNG JSON payload")
)

// Result is one search hit.
type Result struct {
	Title   string
	URL     string
	Content string
}

// Config mirrors apiconfig.SearxngConfig (kept decoupled: no import cycle
// risk and the client is testable standalone).
type Config struct {
	BaseURL          string
	Language         string // "auto"/"" → omit
	Categories       string // "" → omit
	TimeoutMs        int
	APIKeyEnv        string
	AllowInsecureTLS bool
}

// Client queries one SearXNG instance.
type Client struct {
	cfg    Config
	hc     *http.Client
	bearer string

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	results []Result
	expires time.Time
}

// New builds a client; validates the base URL shape.
func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("websearch: base_url is empty")
	}
	if _, err := url.Parse(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("websearch: bad base_url: %w", err)
	}
	if cfg.TimeoutMs <= 0 {
		cfg.TimeoutMs = 10000
	}
	hc := &http.Client{Timeout: time.Duration(cfg.TimeoutMs) * time.Millisecond}
	if cfg.AllowInsecureTLS {
		hc.Transport = newInsecureTransport()
	}
	c := &Client{cfg: cfg, hc: hc, cache: map[string]cacheEntry{}}
	if cfg.APIKeyEnv != "" {
		c.bearer = os.Getenv(cfg.APIKeyEnv)
	}
	return c, nil
}

// Search runs the queries (≤5) with max 2 concurrent requests and merges
// results: dedupe by normalized URL, round-robin interleave, numbered by
// the caller. Transient failures are retried once (SPEC §13.3).
func (c *Client) Search(ctx context.Context, queries []string) ([]Result, error) {
	var qs []string
	seen := map[string]bool{}
	for _, q := range queries {
		q = strings.TrimSpace(q)
		if q == "" || seen[strings.ToLower(q)] {
			continue
		}
		seen[strings.ToLower(q)] = true
		qs = append(qs, q)
	}
	if len(qs) == 0 {
		return nil, errors.New("websearch: no queries")
	}
	if len(qs) > 5 {
		qs = qs[:5]
	}

	per := make([][]Result, len(qs))
	var errs []error
	sem := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for i, q := range qs {
		key := cacheKey(qs, i)
		if rs, ok := c.cached(key); ok {
			per[i] = rs
			continue
		}
		wg.Add(1)
		go func(i int, q string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rs, err := c.queryRetry(ctx, q)
			if err != nil {
				c.mu.Lock()
				errs = append(errs, fmt.Errorf("%q: %w", q, err))
				c.mu.Unlock()
				return
			}
			per[i] = rs
			c.put(key, rs)
		}(i, q)
	}
	wg.Wait()
	if len(errs) == len(qs) {
		return nil, errors.Join(errs...)
	}
	return merge(per), nil
}

func (c *Client) queryRetry(ctx context.Context, q string) ([]Result, error) {
	rs, err := c.searchOne(ctx, q)
	if err == nil || !transient(err) {
		return rs, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(500 * time.Millisecond):
	}
	return c.searchOne(ctx, q)
}

func transient(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) || errors.Is(err, ErrUnreachable) ||
		strings.Contains(err.Error(), "status 5")
}

func (c *Client) searchOne(ctx context.Context, q string) ([]Result, error) {
	u := strings.TrimSuffix(c.cfg.BaseURL, "/") + "/search?"
	v := url.Values{"q": {q}, "format": {"json"}}
	if c.cfg.Language != "" && c.cfg.Language != "auto" {
		v.Set("language", c.cfg.Language)
	}
	if c.cfg.Categories != "" && c.cfg.Categories != "general" {
		v.Set("categories", c.cfg.Categories)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "genisys/0.1 (+https://github.com/larsn03/genisys)")
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrAuthRequired
	case resp.StatusCode == http.StatusForbidden:
		return nil, ErrJSONDisabled
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: status %d", ErrUnreachable, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("searxng: unexpected status %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "json") && !json.Valid(body) {
		return nil, ErrJSONDisabled // HTML answer: format not allowed
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ErrNotSearxng
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

// Probe performs one live round-trip and classifies (SPEC §9.5).
func (c *Client) Probe(ctx context.Context) string {
	_, err := c.searchOne(ctx, "genisys")
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrJSONDisabled):
		return "json-disabled"
	case errors.Is(err, ErrAuthRequired):
		return "auth-required"
	case errors.Is(err, ErrUnreachable):
		return "unreachable"
	default:
		return "not-searxng"
	}
}

// --- merge + cache helpers ---------------------------------------------------

func merge(per [][]Result) []Result {
	seen := map[string]bool{}
	var out []Result
	for round := 0; ; round++ {
		alive := false
		for i := range per {
			if round < len(per[i]) {
				alive = true
				norm := normalizeURL(per[i][round].URL)
				if !seen[norm] {
					seen[norm] = true
					out = append(out, per[i][round])
				}
			}
		}
		if !alive {
			break
		}
	}
	return out
}

// normalizeURL: lowercase scheme+host, drop fragment and tracking params.
func normalizeURL(u string) string {
	low := strings.ToLower(u)
	if i := strings.IndexByte(low, '#'); i >= 0 {
		low = low[:i]
	}
	parsed, err := url.Parse(low)
	if err != nil {
		return low
	}
	q := parsed.Query()
	for _, k := range []string{"utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "fbclid", "gclid", "ref"} {
		q.Del(k)
	}
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

func cacheKey(qs []string, i int) string {
	cp := append([]string(nil), qs...)
	sort.Strings(cp)
	return strings.Join(cp, "|") + "#" + fmt.Sprint(i, len(qs))
}

func (c *Client) cached(key string) ([]Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[key]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.results, true
}

func (c *Client) put(key string, rs []Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[key] = cacheEntry{results: rs, expires: time.Now().Add(24 * time.Hour)}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndex(s[:n], " ")
	if cut <= 0 {
		cut = n
	}
	return s[:cut] + "…"
}

// newInsecureTransport accepts self-signed internal instances — an
// explicit user opt-in via web.searxng.allow_insecure_tls (SPEC §9.5).
func newInsecureTransport() http.RoundTripper {
	return &http.Transport{
		TLSHandshakeTimeout: 5 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, // #nosec G402 — opt-in
	}
}
