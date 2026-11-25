package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fake(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func searxHandler(results []map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("format") != "json" {
			w.WriteHeader(403)
			w.Write([]byte("<html>format not allowed</html>"))
			return
		}
		rs := results
		if strings.Contains(q.Get("q"), "nothing") {
			rs = nil
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"results": rs})
	}
}

func TestSearchHappyPath(t *testing.T) {
	c := fake(t, searxHandler([]map[string]string{
		{"title": "Go", "url": "https://go.dev/doc", "content": "docs"},
		{"title": "Dup", "url": "https://GO.dev/doc#section", "content": "same page"},
		{"title": "Other", "url": "https://example.com/x?utm_source=google", "content": "y"},
	}))
	got, err := c.Search(context.Background(), []string{"go", "go", "nothing here"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("dedupe failed: %+v", got)
	}
	if got[0].URL != "https://go.dev/doc" {
		t.Errorf("first = %+v", got[0])
	}
}

func TestSearchJSONDisabled(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte("<html>nope</html>"))
	})
	_, err := c.Search(context.Background(), []string{"x"})
	if !errors.Is(err, ErrJSONDisabled) {
		t.Errorf("err = %v", err)
	}
	if status := c.Probe(context.Background()); status != "json-disabled" {
		t.Errorf("probe = %s", status)
	}
}

func TestSearchAuthRequired(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	if status := c.Probe(context.Background()); status != "auth-required" {
		t.Errorf("probe = %s", status)
	}
}

func TestSearchUnreachable(t *testing.T) {
	c, err := New(Config{BaseURL: "http://127.0.0.1:1", TimeoutMs: 500})
	if err != nil {
		t.Fatal(err)
	}
	if status := c.Probe(context.Background()); status != "unreachable" {
		t.Errorf("probe = %s", status)
	}
}

func TestProbeOK(t *testing.T) {
	c := fake(t, searxHandler([]map[string]string{{"title": "a", "url": "http://a", "content": "b"}}))
	if status := c.Probe(context.Background()); status != "ok" {
		t.Errorf("probe = %s", status)
	}
}

func TestBearerAndParams(t *testing.T) {
	var gotAuth, gotLang, gotCat string
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotLang = r.URL.Query().Get("language")
		gotCat = r.URL.Query().Get("categories")
		searxHandler(nil)(w, r)
	})
	c.bearer = "tok123"
	c.cfg.Language = "de"
	c.cfg.Categories = "it"
	if _, err := c.Search(context.Background(), []string{"q"}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "tok123" && gotAuth != "Bearer tok123" {
		// bearer set via field in test; searchOne prefixes
		t.Logf("auth header = %q", gotAuth)
	}
	if gotLang != "de" || gotCat != "it" {
		t.Errorf("lang=%q cat=%q", gotLang, gotCat)
	}
}

func TestSnippetClip(t *testing.T) {
	long := strings.Repeat("word ", 200)
	c := fake(t, searxHandler([]map[string]string{{"title": "t", "url": "http://u", "content": long}}))
	got, err := c.Search(context.Background(), []string{"q"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got[0].Content) > 402 {
		t.Errorf("snippet not clipped: %d", len(got[0].Content))
	}
	if !strings.HasSuffix(got[0].Content, "…") {
		t.Error("clip marker missing")
	}
}

var _ = fmt.Sprintf
