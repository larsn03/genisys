// searxng.go — the default, normative backend (SPEC §9.5): any reachable
// instance over the JSON API; 403/HTML is reported as json-disabled, never
// silently fallen back.

package websearch

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type searxng struct {
	cfg    Config
	hc     *http.Client
	bearer string
}

func newSearxng(cfg Config) (Provider, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("websearch: searxng base_url is empty")
	}
	if _, err := url.Parse(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("websearch: bad base_url: %w", err)
	}
	hc := &http.Client{Timeout: time.Duration(cfg.TimeoutMs) * time.Millisecond}
	if cfg.AllowInsecureTLS {
		hc.Transport = newInsecureTransport()
	}
	s := &searxng{cfg: cfg, hc: hc}
	if cfg.APIKeyEnv != "" {
		s.bearer = os.Getenv(cfg.APIKeyEnv)
	}
	return s, nil
}

func (s *searxng) Name() string { return "searxng" }

func newInsecureTransport() *http.Transport {
	base, _ := http.DefaultTransport.(*http.Transport) // always true unless user-swapped
	tr := base.Clone()
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	return tr
}

func (s *searxng) SearchOne(ctx context.Context, q string) ([]Result, error) {
	u := strings.TrimSuffix(s.cfg.BaseURL, "/") + "/search?"
	v := url.Values{"q": {q}, "format": {"json"}}
	if s.cfg.Language != "" && s.cfg.Language != "auto" {
		v.Set("language", s.cfg.Language)
	}
	if s.cfg.Categories != "" && s.cfg.Categories != "general" {
		v.Set("categories", s.cfg.Categories)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if s.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+s.bearer)
	}
	resp, err := s.hc.Do(req)
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

// Probe classifies the instance (SPEC §9.5 reachability check).
func (s *searxng) Probe(ctx context.Context) string {
	_, err := s.SearchOne(ctx, "genisys")
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
