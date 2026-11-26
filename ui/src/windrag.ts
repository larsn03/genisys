// Edge-resize cursor feedback for the frameless native window.
//
// The actual drag/resize/maximise gestures are handled natively (a
// CAPTURE-phase GtkGestureClick in internal/desktop/corners_gtk4.go) —
// Wails' JS-message drag path deadlocks/kills Wayland clients, so we
// deliberately never send wails:drag / wails:resize messages. All that
// remains here is painting the right cursor when the pointer is over a
// window edge, mirroring the native hit zones (5px edges, 15px corners).

const HANDLE = 5;
const CORNER = 15;

const CURSORS: Record<string, string> = {
  n: "ns-resize",
  s: "ns-resize",
  e: "ew-resize",
  w: "ew-resize",
  nw: "nwse-resize",
  se: "nwse-resize",
  ne: "nesw-resize",
  sw: "nesw-resize",
};

function edgeAt(x: number, y: number): string {
  const W = window.innerWidth;
  const H = window.innerHeight;
  const r = W - x;
  const b = H - y;
  if (y < CORNER && x < CORNER) return "nw";
  if (y < CORNER && r < CORNER) return "ne";
  if (b < CORNER && r < CORNER) return "se";
  if (b < CORNER && x < CORNER) return "sw";
  if (x < HANDLE) return "w";
  if (y < HANDLE) return "n";
  if (r < HANDLE) return "e";
  if (b < HANDLE) return "s";
  return "";
}

export function installWindowDrag() {
  window.addEventListener(
    "mousemove",
    (e) => {
      document.body.style.cursor = CURSORS[edgeAt(e.clientX, e.clientY)] ?? "";
    },
    { passive: true },
  );
}
