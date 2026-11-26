//go:build wails && gtk3

package desktop

import "unsafe"

// Rounded frameless corners and native gestures are GTK4 CSS/controller
// features; the gtk3 tag path keeps square corners and Wails' own
// (X11-flavoured) drag path.
func installRoundedFramelessCSS() {}

func installWindowGestures(native unsafe.Pointer) {}
