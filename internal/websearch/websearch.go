// Package websearch implements the pluggable web_search backends
// (SPEC §9.5/§16): SearXNG (default, normative), Tavily, Brave. Results
// are merged, deduplicated, and cached 24 h. Queries go only to the
// configured provider — no silent third-party fallback.
package websearch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
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

// Config selects and parameterizes a provider (kept decoupled from
// apiconfig; the client is testable standalone).
type Config struct {
	Provider         string // "" | searxng | tavily | brave
	BaseURL          string // searxng: required; tavily/brave: optional override
	Language         string // "auto"/"" → omit (searxng)
	Categories       string // "" → omit (searxng)
	TimeoutMs        int
	APIKeyEnv        string // bearer/subscription token env var
	AllowInsecureTLS bool
}

// Provider is a search backend behind the common contract (SPEC §16).
type Provider interface {
	Name() string
	SearchOne(ctx context.Context, q string) ([]Result, error)
	Probe(ctx context.Context) string
}

// Client wraps one provider with merge + 24 h cache (SPEC §16).
type Client struct {
	p Provider

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	results []Result
	expires time.Time
}

// New builds a client for the configured provider.
func New(cfg Config) (*Client, error) {
	if cfg.TimeoutMs <= 0 {
		cfg.TimeoutMs = 10000
	}
	var p Provider
	var err error
	switch cfg.Provider {
	case "", "searxng":
		p, err = newSearxng(cfg)
	case "tavily":
		p, err = newTavily(cfg)
	case "brave":
		p, err = newBrave(cfg)
	default:
		return nil, fmt.Errorf("websearch: unknown provider %q", cfg.Provider)
	}
	if err != nil {
		return nil, err
	}
	return &Client{p: p, cache: map[string]cacheEntry{}}, nil
}

// ProviderName reports the active backend (settings UI).
func (c *Client) ProviderName() string { return c.p.Name() }

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
	rs, err := c.p.SearchOne(ctx, q)
	if err == nil || !transient(err) {
		return rs, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(500 * time.Millisecond):
	}
	return c.p.SearchOne(ctx, q)
}

func transient(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) || errors.Is(err, ErrUnreachable) ||
		strings.Contains(err.Error(), "status 5")
}

// Probe performs one live round-trip and classifies (SPEC §9.5).
func (c *Client) Probe(ctx context.Context) string { return c.p.Probe(ctx) }

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
