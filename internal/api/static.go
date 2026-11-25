// static.go — serves the embedded SPA (SPEC §19/§22): assets under
// /assets, SPA fallback to index.html for client routes, API paths
// untouched.

package api

import (
	"io/fs"
	"net/http"
	"strings"
)

func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	if s.ui == nil {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if f, err := s.ui.Open(path); err == nil {
		defer f.Close()
		if d, serr := fs.Stat(s.ui, path); serr == nil && !d.IsDir() {
			http.ServeContent(w, r, path, d.ModTime(), f.(interface {
				Read([]byte) (int, error)
				Seek(int64, int) (int64, error)
			}))
			return
		}
	}
	// SPA fallback
	idx, err := fs.ReadFile(s.ui, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(idx)
}
