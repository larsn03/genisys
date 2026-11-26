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
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Controls are the native window actions Run publishes so the SPA can
// draw its own titlebar. They are plain funcs (no import cycle: the API
// layer defines the interface, cmd/genisys adapts these to it). Run
// populates the struct before showing the window.
type Controls struct {
	Minimize       func()
	ToggleMaximize func()
	IsMaximized    func() bool
	Close          func()
}

// Options configures the main window.
type Options struct {
	Title string
	URL   string // full server URL incl. ?token=
	Ctl   *Controls
}

// Run opens the native window and blocks until the app quits (last
// window closed → Wails quits by default on Linux/Windows/macOS).
func Run(o Options) error {
	app := application.New(application.Options{
		Name:        "Genisys",
		Description: "AI agent for OpenAI-compatible endpoints",
		Linux: application.LinuxOptions{
			ProgramName:   "genisys",
			ApplicationID: "de.larsnieswandt.Genisys", // Wayland app_id; matches StartupWMClass
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	// Re-round the frameless window at GTK level (see corners_gtk4.go);
	// ApplicationStarted fires once GTK is initialised, before paint.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted,
		func(*application.ApplicationEvent) { installRoundedFramelessCSS() })

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
	installWindowGestures(win.NativeWindow())

	if o.Ctl != nil {
		o.Ctl.Minimize = func() { win.Minimise() }
		o.Ctl.ToggleMaximize = func() { win.ToggleMaximise() }
		o.Ctl.IsMaximized = func() bool { return win.IsMaximised() }
		o.Ctl.Close = func() { win.Close() }
	}
	win.Center() // Center returns nothing (v3 API)
	win.Show()

	// Robust shutdown: don't depend on app.Run() unwinding cleanly when
	// the last window closes (its GApplication teardown has proven
	// flaky on GNOME/Wayland). Watch the window's own closing event and
	// return ourselves; app.Run gets a grace period to exit naturally.
	// The window is already being destroyed by Wails' default
	// WindowClosing listener, so returning here is safe.
	closed := make(chan struct{})
	win.OnWindowEvent(events.Common.WindowClosing,
		func(*application.WindowEvent) { close(closed) })

	runErr := make(chan error, 1)
	go func() { runErr <- app.Run() }()
	select {
	case err := <-runErr:
		return err
	case <-closed:
		select {
		case err := <-runErr:
			return err
		case <-time.After(2 * time.Second):
			return nil
		}
	}
}
