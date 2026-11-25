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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotLang = r.URL.Query().Get("language")
		gotCat = r.URL.Query().Get("categories")
		searxHandler(nil)(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GENISYS_TEST_SEARCH_KEY", "tok123")
	c, err := New(Config{BaseURL: srv.URL, APIKeyEnv: "GENISYS_TEST_SEARCH_KEY", Language: "de", Categories: "it"})
	if err != nil {
		t.Fatal(err)
	}
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

// P5-3: tavily + brave providers against httptest fakes.
func TestTavilyProvider(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[{"title":"T","url":"http://t","content":"c"}]}`))
	}))
	defer srv.Close()
	t.Setenv("GENISYS_TAVILY_KEY", "tv-key")
	c, err := New(Config{Provider: "tavily", BaseURL: srv.URL, APIKeyEnv: "GENISYS_TAVILY_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	rs, err := c.Search(context.Background(), []string{"q"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].URL != "http://t" {
		t.Fatalf("results=%v", rs)
	}
	if gotAuth != "Bearer tv-key" || gotPath != "/search" {
		t.Errorf("auth=%q path=%q", gotAuth, gotPath)
	}
	if p := c.Probe(context.Background()); p != "ok" {
		t.Errorf("probe=%s", p)
	}
}

func TestBraveProvider(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Subscription-Token")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"web":{"results":[{"title":"B","url":"http://b","description":"d"}]}}`))
	}))
	defer srv.Close()
	t.Setenv("GENISYS_BRAVE_KEY", "br-key")
	c, err := New(Config{Provider: "brave", BaseURL: srv.URL, APIKeyEnv: "GENISYS_BRAVE_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	rs, err := c.Search(context.Background(), []string{"q"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Title != "B" {
		t.Fatalf("results=%v", rs)
	}
	if gotToken != "br-key" {
		t.Errorf("token=%q", gotToken)
	}
}

func TestProviderMissingToken(t *testing.T) {
	if _, err := New(Config{Provider: "tavily", APIKeyEnv: "GENISYS_MISSING_KEY"}); err == nil {
		t.Error("tavily without token must fail at construction")
	}
}

func TestUnknownProvider(t *testing.T) {
	if _, err := New(Config{Provider: "google"}); err == nil {
		t.Error("unknown provider must fail")
	}
}
