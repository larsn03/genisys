//go:build wails && !gtk3

package desktop

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>

// Wails' frameless mode zeroes the Adwaita window radius via an
// APPLICATION-priority CSS class (.wails-frameless { border-radius: 0 }).
// We re-round at USER priority (which outranks it) and make the window
// background transparent so the corner arcs show the desktop; the
// webview stays fully opaque (transparent WebKitGTK backgrounds render
// black on some Mesa configs). GTK4 clips child widgets — including the
// webview — to the window's rounded outline.
static void genisys_rounded_frameless(void) {
	GdkDisplay *d = gdk_display_get_default();
	if (!d) {
		return;
	}
	GtkCssProvider *p = gtk_css_provider_new();
	gtk_css_provider_load_from_string(p,
		"window.wails-frameless {"
		"  border-radius: 12px;"
		"  background-color: rgba(0, 0, 0, 0);"
		"}"
		"window.wails-frameless.maximized,"
		"window.wails-frameless.tiled,"
		"window.wails-frameless.fullscreen {"
		"  border-radius: 0;"
		"}");
	gtk_style_context_add_provider_for_display(
		d, GTK_STYLE_PROVIDER(p), GTK_STYLE_PROVIDER_PRIORITY_USER);
	g_object_unref(p);
}
*/
import "C"

func installRoundedFramelessCSS() {
	C.genisys_rounded_frameless()
}
