//go:build wails && gtk3

package desktop

// Rounded frameless corners are a GTK4 CSS feature; the gtk3 build tag
// path keeps square corners (webkit2gtk-4.1 transparency differs).
func installRoundedFramelessCSS() {}
