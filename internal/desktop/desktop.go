//go:build wails

// Package desktop wraps the Wails v3 native window (SPEC Q1 resolved:
// web-first core, optional native shell). The window is a thin frame
// pointed at our own localhost server — the REST/WS API stays the single
// source of truth, so `genisys window` and `genisys serve` share
// everything except the chrome around the UI.
//
// Build with: go build -tags wails (CGO on; needs GTK4 + WebKitGTK 6.0
// dev headers, or -tags "wails gtk3" for the WebKit2GTK-4.1 path).
package desktop

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Options configures the main window.
type Options struct {
	Title string
	URL   string // full server URL incl. ?token=
}

// Shell is bound into the webview so the SPA can draw its own window
// chrome (the window is Frameless — SPEC G12: the app owns its chrome).
// The frontend discovers it by feature-scanning window.go (see
// ui/src/shell.ts), so renaming this type or moving packages is safe.
type Shell struct{ win *application.WebviewWindow }

// Minimize iconifies the window.
func (s *Shell) Minimize() {
	if s.win != nil {
		s.win.Minimise()
	}
}

// ToggleMaximize maximises or restores the window.
func (s *Shell) ToggleMaximize() {
	if s.win != nil {
		s.win.ToggleMaximise()
	}
}

// IsMaximized reports the current maximised state (titlebar icon sync).
func (s *Shell) IsMaximized() bool {
	return s.win != nil && s.win.IsMaximised()
}

// Close closes the window; last window closed quits the app (server
// shutdown hooks run, so the transcript is flushed).
func (s *Shell) Close() {
	if s.win != nil {
		s.win.Close()
	}
}

// Run opens the native window and blocks until the app quits (last
// window closed → Wails quits by default on Linux/Windows/macOS).
func Run(o Options) error {
	shell := &Shell{}
	app := application.New(application.Options{
		Name:        "Genisys",
		Description: "AI agent for OpenAI-compatible endpoints",
		Services:    []application.Service{application.NewService(shell)},
		Linux: application.LinuxOptions{
			ProgramName: "genisys",
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            o.Title,
		Frameless:        true, // custom titlebar rendered by the SPA
		Width:            1440,
		Height:           900,
		MinWidth:         1024,
		MinHeight:        640,
		URL:              o.URL,
		BackgroundColour: application.NewRGBA(19, 20, 25, 255), // match --color-bg (dark)
	})
	shell.win = win
	win.Center() // Center returns nothing (v3 API)
	win.Show()

	return app.Run()
}
