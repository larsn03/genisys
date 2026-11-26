// Package api is the HTTP + WebSocket front-end for `genisys serve`
// (SPEC §19/§21): REST for CRUD, WS for the live event spine and commands.
// Single-user: a bearer token minted at startup guards everything except
// /api/version; the server binds localhost by default.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/larsn03/genisys/internal/app"
	"github.com/larsn03/genisys/internal/supervisor"
)

// Server bundles app + supervisor + auth.
// WindowController exposes native window actions to the SPA's custom
// titlebar (frameless desktop build). Wired by cmd/genisys when the
// Wails shell is live; nil in headless/browser mode → routes 404.
type WindowController interface {
	Minimize()
	ToggleMaximize()
	IsMaximized() bool
	Close()
}

type Server struct {
	App        *app.App
	Sup        *supervisor.Supervisor
	Token      string
	ConfigPath string
	WinCtl     WindowController
	ui         fs.FS
	log        *log.Logger
	mux        *http.ServeMux
	upgrader   websocket.Upgrader
}

// New builds the server; uiFS may be nil (API-only mode).
func New(a *app.App, cfgPath string, uiFS fs.FS, logger *log.Logger) *Server {
	token := makeToken()
	s := &Server{
		App: a, Sup: supervisor.New(a, 4), Token: token,
		ConfigPath: cfgPath, ui: uiFS, log: logger, mux: http.NewServeMux(),
		upgrader: websocket.Upgrader{
			ReadBufferSize: 4096, WriteBufferSize: 4096,
			// single-user localhost app; origin check would block the
			// default-browser flow, token query param carries auth
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
	s.routes()
	return s
}

// Handler returns the middleware-wrapped mux.
func (s *Server) Handler() http.Handler {
	return s.auth(s.mux)
}

func makeToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// auth: bearer token (header or ?token=) on /api/* and /ws.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/api/version" || !strings.HasPrefix(path, "/api/") && path != "/ws" {
			next.ServeHTTP(w, r) // static shell needs no token (it fetches with it)
			return
		}
		tok := ""
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			tok = strings.TrimPrefix(h, "Bearer ")
		} else {
			tok = r.URL.Query().Get("token")
		}
		if tok != s.Token {
			writeErr(w, http.StatusUnauthorized, "bad or missing token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- json helpers -------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(v)
}
