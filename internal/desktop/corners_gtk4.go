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
		// Fully transparent => GTK never paints the window surface =>
		// no commit to acknowledge configure requests => mutter's
		// interactive resize glitches ("Buggy client" workaround).
		// Alpha 0.01 forces a paint+commit and is visually identical.
		"  background-color: rgba(0, 0, 0, 0.01);"
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

// drag-begin fires once the drag threshold is exceeded, while the
// motion event is still being processed — the implicit grab serial is
// valid here, which is exactly what Wayland requires for move/resize.
static void on_drag_begin(GtkGestureDrag *g, double dx, double dy,
                          GtkWidget *target) {
	(void)dx;
	(void)dy;
	DragState *st = g_object_get_data(G_OBJECT(target), "genisys-drag");
	// Press position + button straight from the gesture: no reliance on
	// GtkGesture::down (renamed away in GTK 4.24).
	double sx = 0, sy = 0;
	gtk_gesture_drag_get_start_point(GTK_GESTURE_DRAG(g), &sx, &sy);
	GdkEvent *ev = gtk_gesture_get_last_event(GTK_GESTURE(g), NULL);
	st->button = ev ? gdk_button_event_get_button(ev) : 1;
	st->px = sx;
	st->py = sy;
	st->zone = zone_at(target, sx, sy);
	if (st->zone == Z_NONE)
		return;
	g_printerr("[genisys-gesture] press+drag zone=%d at %.0f,%.0f\n",
	           st->zone, sx, sy);
	// The webview is a GtkNative with its OWN child surface; move/resize
	// must go to the toplevel surface of the window.
	GdkDevice *dev = gtk_gesture_get_device(GTK_GESTURE(g));
	if (!dev) {
		GdkSeat *seat = gdk_display_get_default_seat(
		                        gdk_display_get_default());
		dev = gdk_seat_get_pointer(seat);
	}
	GtkWidget *win = gtk_widget_get_ancestor(target, GTK_TYPE_WINDOW);
	GdkSurface *surf = gtk_native_get_surface(GTK_NATIVE(win));
	if (!dev || !GDK_IS_SURFACE(surf) || !GDK_IS_TOPLEVEL(surf)) {
		g_printerr("[genisys-gesture] abort: dev=%p surf=%p toplevel=%d\n",
		           (void *)dev, (void *)surf,
		           surf ? (int)GDK_IS_TOPLEVEL(surf) : -1);
		return;
	}
	st->started = 1;
	g_printerr("[genisys-gesture] begin %s zone=%d\n",
	           st->zone == Z_TITLE ? "move" : "resize", st->zone);
	if (st->zone == Z_TITLE) {
		gdk_toplevel_begin_move(GDK_TOPLEVEL(surf), dev, st->button,
		                        st->px, st->py, GDK_CURRENT_TIME);
	} else {
		// Mutter ignores resize requests on maximized surfaces; GNOME
		// semantics are "resize un-maximizes" — do that first.
		if (gtk_window_is_maximized(GTK_WINDOW(win)))
			gtk_window_unmaximize(GTK_WINDOW(win));
		gdk_toplevel_begin_resize(GDK_TOPLEVEL(surf), edge_of(st->zone), dev,
		                          st->button, st->px, st->py,
		                          GDK_CURRENT_TIME);
	}
}

// Click gesture: only used for double-click-to-maximize.
static void on_released(GtkGestureClick *g, gint n_press, gint button,
                        double x, double y, GtkWidget *target) {
	(void)n_press;
	DragState *st = g_object_get_data(G_OBJECT(target), "genisys-drag");
	st->zone = Z_NONE;
	st->started = 0;
	if (button != 1)
		return;
	int width = gtk_widget_get_width(target);
	if (gtk_gesture_single_get_current_button(GTK_GESTURE_SINGLE(g)) == 1 &&
	    n_press == 2 && y <= TB_H && x < width - TB_BTNS) {
		GtkWidget *win = gtk_widget_get_ancestor(target, GTK_TYPE_WINDOW);
		if (gtk_window_is_maximized(GTK_WINDOW(win)))
			gtk_window_unmaximize(GTK_WINDOW(win));
		else
			gtk_window_maximize(GTK_WINDOW(win));
	}
}

static GtkWidget *find_descendant(GtkWidget *widget, const char *type_name) {
	if (G_IS_OBJECT(widget) &&
	    g_type_is_a(G_OBJECT_TYPE(widget), g_type_from_name(type_name)))
		return widget;
	for (GtkWidget *c = gtk_widget_get_first_child(widget); c;
	     c = gtk_widget_get_next_sibling(c)) {
		GtkWidget *f = find_descendant(c, type_name);
		if (f)
			return f;
	}
	return NULL;
}

static void genisys_install_gestures(GtkWindow *win) {
	GtkWidget *w = GTK_WIDGET(win);
	// Attach to the webview, not the window: the webview receives all
	// pointer traffic, and a CAPTURE controller on it runs before
	// WebKitGTK's own handling. A window-level controller sees the
	// press but never the motion (the webview consumes it). The webview
	// fills the window, so its coordinates are the page's.
	GtkWidget *target = find_descendant(w, "WebKitWebView");
	if (!target)
		target = w;
	g_printerr("[genisys-gesture] installed on %s; display=%s\n",
	        G_OBJECT_TYPE_NAME(target),
	        gdk_display_get_name(gdk_display_get_default()));
	DragState *st = g_new0(DragState, 1);
	g_object_set_data_full(G_OBJECT(target), "genisys-drag", st, g_free);

	// Drag gesture (capture): drives compositor move/resize.
	GtkGestureDrag *drag = (GtkGestureDrag *) gtk_gesture_drag_new();
	gtk_event_controller_set_propagation_phase(GTK_EVENT_CONTROLLER(drag),
	                                           GTK_PHASE_CAPTURE);
	g_signal_connect(drag, "drag-begin", G_CALLBACK(on_drag_begin), target);
	gtk_widget_add_controller(target, GTK_EVENT_CONTROLLER(drag));

	// Click gesture (capture): double-click on the titlebar = maximize.
	GtkGestureClick *click = (GtkGestureClick *) gtk_gesture_click_new();
	gtk_event_controller_set_propagation_phase(GTK_EVENT_CONTROLLER(click),
	                                           GTK_PHASE_CAPTURE);
	g_signal_connect(click, "released", G_CALLBACK(on_released), target);
	gtk_widget_add_controller(target, GTK_EVENT_CONTROLLER(click));
}

// ── Maximise/unmaximise without Wails' wrappers ─────────────────────
// Wails' UnMaximise() deadlocks the main loop on Linux:
// EnableSizeConstraints -> InvokeSync(closure) -> SetMinSize -> Size()
// -> InvokeSyncWithResult, i.e. the main loop waits for itself.
// gtk_window_maximize/unmaximize alone does everything useful (state
// flags, configure); Wails' isMaximised reads the live GTK state, so
// bypassing its wrappers keeps state consistent.

static gboolean cb_maximise(gpointer d) {
	gtk_window_maximize(GTK_WINDOW(d));
	return G_SOURCE_REMOVE;
}
static gboolean cb_unmaximise(gpointer d) {
	gtk_window_unmaximize(GTK_WINDOW(d));
	return G_SOURCE_REMOVE;
}

static void genisys_set_maximised(GtkWindow *win, gboolean maximised) {
	g_idle_add(maximised ? cb_maximise : cb_unmaximise, win);
}

static gboolean install_gestures_cb(gpointer data) {
	genisys_install_gestures(GTK_WINDOW(data));
	return G_SOURCE_REMOVE;
}

static void genisys_schedule_gestures(GtkWindow *win) {
	g_idle_add(install_gestures_cb, win);
}

static void genisys_schedule_set_maximised(GtkWindow *win, gboolean max) {
	genisys_set_maximised(win, max);
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

// setMaximised toggles the maximized state via plain GTK, dodging the
// Wails UnMaximise main-loop deadlock. native comes from
// WebviewWindow.NativeWindow().
func setMaximised(native unsafe.Pointer, max bool) {
	if native == nil {
		return
	}
	C.genisys_schedule_set_maximised((*C.GtkWindow)(native), C.gboolean(bool2i(max)))
}

func bool2i(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
