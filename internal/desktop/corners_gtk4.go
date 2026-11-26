//go:build wails && !gtk3

package desktop

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>

// ── Rounded frameless window ────────────────────────────────────────
// Wails' frameless mode zeroes the Adwaita window radius via an
// APPLICATION-priority CSS class (.wails-frameless { border-radius: 0 }).
// We re-round at USER priority (which outranks it) and make the window
// background transparent so the corner arcs show the desktop; the
// webview stays fully opaque (transparent WebKitGTK backgrounds render
// black on some Mesa configs). GTK4 clips child widgets — including the
// webview — to the window's rounded outline.

static gboolean install_css_cb(gpointer data) {
	(void)data;
	GdkDisplay *d = gdk_display_get_default();
	if (!d) return G_SOURCE_REMOVE;
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
	return G_SOURCE_REMOVE;
}

static void genisys_schedule_css(void) { g_idle_add(install_css_cb, NULL); }

// ── Native titlebar drag / edge resize / double-click maximise ──────
// Wails v3.0.0's own drag path is broken on Wayland: it round-trips a
// JS message through a goroutine + g_idle and calls
// gdk_toplevel_begin_move long after the press, with GDK_CURRENT_TIME;
// if the compositor sees a stale/zero serial it raises an xdg-shell
// protocol error and GNOME kills the client ("not responding"). We
// attach our own CAPTURE-phase click gesture to the window and start
// the move/resize *synchronously inside motion-event processing* once a
// 4px threshold is exceeded — the canonical Wayland pattern: GDK
// attaches the press' implicit grab serial and the request is valid.
//
// Uses gdk_toplevel_begin_move/resize directly (gtk_window_begin_move_
// drag was removed in GTK 4.24; the GDK calls work on 4.14+ alike).

#define TB_H      38.0  // custom titlebar height (CSS px == GTK logical px)
#define TB_BTNS   140.0 // window-control buttons on the right (3×46 + slack)
#define HANDLE    5.0   // resize edge band
#define CORNER    15.0  // corner grab area
#define THRESH2   16.0  // 4px drag threshold, squared

enum { Z_NONE = 0, Z_TITLE, Z_NW, Z_NE, Z_SE, Z_SW, Z_W, Z_N, Z_E, Z_S };

typedef struct {
	double px, py; // press, widget coords
	int button;
	int zone;
	int started;
} DragState;

static int zone_at(GtkWidget *win, double x, double y) {
	int w = gtk_widget_get_width(win);
	int h = gtk_widget_get_height(win);
	double r = w - x, b = h - y;
	if (y < CORNER && x < CORNER) return Z_NW;
	if (y < CORNER && r < CORNER) return Z_NE;
	if (b < CORNER && r < CORNER) return Z_SE;
	if (b < CORNER && x < CORNER) return Z_SW;
	if (x < HANDLE) return Z_W;
	if (y < HANDLE) return Z_N;
	if (r < HANDLE) return Z_E;
	if (b < HANDLE) return Z_S;
	if (y <= TB_H && x < w - TB_BTNS) return Z_TITLE;
	return Z_NONE;
}

static GdkSurfaceEdge edge_of(int zone) {
	switch (zone) {
	case Z_NW: return GDK_SURFACE_EDGE_NORTH_WEST;
	case Z_NE: return GDK_SURFACE_EDGE_NORTH_EAST;
	case Z_SE: return GDK_SURFACE_EDGE_SOUTH_EAST;
	case Z_SW: return GDK_SURFACE_EDGE_SOUTH_WEST;
	case Z_W:  return GDK_SURFACE_EDGE_WEST;
	case Z_N:  return GDK_SURFACE_EDGE_NORTH;
	case Z_E:  return GDK_SURFACE_EDGE_EAST;
	default:   return GDK_SURFACE_EDGE_SOUTH;
	}
}

static void on_pressed(GtkGestureClick *g, gint n_press, double x, double y,
                       gpointer user_data) {
	(void)n_press;
	if (gtk_gesture_single_get_current_button(GTK_GESTURE_SINGLE(g)) != 1)
		return;
	GtkWidget *win = GTK_WIDGET(user_data);
	DragState *st = g_object_get_data(G_OBJECT(win), "genisys-drag");
	st->px = x;
	st->py = y;
	st->button = 1;
	st->started = 0;
	st->zone = zone_at(win, x, y);
	g_printerr("[genisys-gesture] press zone=%d at %.0f,%.0f (w=%d h=%d)\n",
	        st->zone, x, y, gtk_widget_get_width(win),
	        gtk_widget_get_height(win));
}

static void on_update(GtkGestureClick *g, guint n_press, double x, double y,
                      gpointer user_data) {
	(void)n_press;
	GtkWidget *win = GTK_WIDGET(user_data);
	DragState *st = g_object_get_data(G_OBJECT(win), "genisys-drag");
	if (st->started || st->zone == Z_NONE) return;
	double dx = x - st->px, dy = y - st->py;
	if (dx * dx + dy * dy < THRESH2) return;
	st->started = 1; // fire exactly once, inside this event

	GtkNative *native = gtk_widget_get_native(win);
	GdkSurface *surf = native ? gtk_native_get_surface(native) : NULL;
	if (!surf || !GDK_IS_TOPLEVEL(surf)) return;
	GdkDevice *dev = gtk_gesture_get_device(GTK_GESTURE(g));
	if (!dev) return;

	g_printerr("[genisys-gesture] start %s zone=%d (dev=%s)\n",
	        st->zone == Z_TITLE ? "move" : "resize", st->zone,
	        dev ? gdk_device_get_name(dev) : "none");
	if (st->zone == Z_TITLE)
		gdk_toplevel_begin_move(GDK_TOPLEVEL(surf), dev, st->button,
		                        x, y, GDK_CURRENT_TIME);
	else
		gdk_toplevel_begin_resize(GDK_TOPLEVEL(surf), edge_of(st->zone), dev,
		                          st->button, x, y, GDK_CURRENT_TIME);
	g_printerr("[genisys-gesture] request sent\n");
}

static void on_released(GtkGestureClick *g, gint n_press, double x, double y,
                        gpointer user_data) {
	(void)g;
	GtkWidget *win = GTK_WIDGET(user_data);
	DragState *st = g_object_get_data(G_OBJECT(win), "genisys-drag");
	if (!st->started && n_press == 2 && y <= TB_H &&
	    x < gtk_widget_get_width(win) - TB_BTNS) {
		// double-click on the titlebar strip → toggle maximised
		if (gtk_window_is_maximized(GTK_WINDOW(win)))
			gtk_window_unmaximize(GTK_WINDOW(win));
		else
			gtk_window_maximize(GTK_WINDOW(win));
	}
	st->zone = Z_NONE;
	st->started = 0;
}

static void genisys_install_gestures(GtkWindow *win) {
	GtkWidget *w = GTK_WIDGET(win);
	g_printerr("[genisys-gesture] installed; display=%s\n",
	        gdk_display_get_name(gdk_display_get_default()));
	DragState *st = g_new0(DragState, 1);
	g_object_set_data_full(G_OBJECT(w), "genisys-drag", st, g_free);
	GtkGesture *g = gtk_gesture_click_new();
	gtk_event_controller_set_propagation_phase(GTK_EVENT_CONTROLLER(g),
	                                           GTK_PHASE_CAPTURE);
	g_signal_connect(g, "pressed", G_CALLBACK(on_pressed), w);
	g_signal_connect(g, "update", G_CALLBACK(on_update), w);
	g_signal_connect(g, "released", G_CALLBACK(on_released), w);
	// note: GtkGesture::cancelled was removed in GTK 4.24; on_pressed
	// resets state, so a missed release self-heals on the next press.
	gtk_widget_add_controller(w, GTK_EVENT_CONTROLLER(g));
}

static gboolean install_gestures_cb(gpointer data) {
	genisys_install_gestures(GTK_WINDOW(data));
	return G_SOURCE_REMOVE;
}

static void genisys_schedule_gestures(GtkWindow *win) {
	g_idle_add(install_gestures_cb, win);
}
*/
import "C"

import (
	"log"
	"unsafe"
)

func installRoundedFramelessCSS() {
	C.genisys_schedule_css()
}

// installWindowGestures attaches the native drag/resize/maximise gesture
// to the GtkWindow behind a Wails window (unsafe.Pointer from
// WebviewWindow.NativeWindow()). Deferred to the main loop via g_idle.
func installWindowGestures(native unsafe.Pointer) {
	log.Printf("gestures: native=%v", native)
	if native == nil {
		return
	}
	C.genisys_schedule_gestures((*C.GtkWindow)(native))
	log.Print("gestures: idle queued")
}
