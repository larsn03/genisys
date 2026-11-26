// Frameless-window move/resize shim.
//
// Wails' full JS runtime (which normally implements titlebar drag and
// edge-resize) is only served to pages hosted by Wails' own asset
// server; Genisys loads its UI from its own origin, so we reproduce the
// relevant behaviours here on top of the core-injected
// `window._wails.invoke` message channel. Message protocol and zone
// sizes mirror Wails v3.0.0's runtime drag.ts exactly — including the
// `detail === 1` guard (only the *first* mousedown starts a drag; the
// compositor's move grab would otherwise eat the double-click) and the
// separate dblclick → "wails:drag:doubleclick" path.

const HANDLE = 5; // system.resizeHandle{Width,Height} default
const CORNER_EXTRA = 10; // resizeCornerExtra default

function invoke(msg: string) {
  const w = window as unknown as {
    _wails?: { invoke?: (m: string) => void };
  };
  try {
    w._wails?.invoke?.(msg);
  } catch {
    /* webview gone */
  }
}

function edgeAt(x: number, y: number): string {
  const W = window.innerWidth;
  const H = window.innerHeight;
  const r = W - x;
  const b = H - y;
  const inR = r < HANDLE + CORNER_EXTRA;
  const inL = x < HANDLE + CORNER_EXTRA;
  const inT = y < HANDLE + CORNER_EXTRA;
  const inB = b < HANDLE + CORNER_EXTRA;
  // corners first
  if (inR && inB) return "se-resize";
  if (inL && inB) return "sw-resize";
  if (inL && inT) return "nw-resize";
  if (inT && inR) return "ne-resize";
  // then edges
  if (r < HANDLE) return "e-resize";
  if (x < HANDLE) return "w-resize";
  if (y < HANDLE) return "n-resize";
  if (b < HANDLE) return "s-resize";
  return "";
}

function isDraggable(t: EventTarget | null): boolean {
  if (!(t instanceof Element)) return false;
  return (
    getComputedStyle(t).getPropertyValue("--wails-draggable").trim() === "drag"
  );
}

let edge = "";

export function installWindowDrag() {
  window.addEventListener(
    "mousemove",
    (e) => {
      edge = edgeAt(e.clientX, e.clientY);
      document.body.style.cursor = edge;
    },
    { passive: true },
  );

  window.addEventListener(
    "mousedown",
    (e) => {
      if (e.button !== 0) return;
      // Only the first press of a press-sequence arms a gesture; later
      // presses (detail ≥ 2) belong to double/triple-click handling.
      if (e.detail !== 1) return;
      if (edge) {
        invoke("wails:resize:" + edge);
        return;
      }
      if (isDraggable(e.target)) {
        e.preventDefault(); // no text selection while dragging the window
        invoke("wails:drag");
      }
    },
    true,
  );

  // Double-click on the drag region → native maximise toggle. (If the
  // compositor's move grab swallows the second press, this simply never
  // fires and the drag path already handled the gesture.)
  window.addEventListener(
    "dblclick",
    (e) => {
      if (isDraggable(e.target)) invoke("wails:drag:doubleclick");
    },
    true,
  );
}
