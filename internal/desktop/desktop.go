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

// Run opens the native window and blocks until the app quits (last
// window closed → Wails quits by default on Linux/Windows/macOS).
func Run(o Options) error {
	app := application.New(application.Options{
		Name:        "Genisys",
		Description: "AI agent for OpenAI-compatible endpoints",
		Linux: application.LinuxOptions{
			ProgramName: "genisys",
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            o.Title,
		Width:            1440,
		Height:           900,
		MinWidth:         1024,
		MinHeight:        640,
		URL:              o.URL,
		BackgroundColour: application.NewRGBA(19, 20, 25, 255), // match --color-bg (dark)
	}).Center().Show()

	return app.Run()
}
