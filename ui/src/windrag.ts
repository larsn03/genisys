// Frameless-window move/resize shim.
//
// Wails' full JS runtime (which normally implements titlebar drag and
// edge-resize) is only served to pages hosted by Wails' own asset
// server; Genisys loads its UI from its own origin, so we reproduce the
// two relevant behaviours here on top of the core-injected
// `window._wails.invoke` message channel:
//
//   mousedown on [--wails-draggable: drag] → "wails:drag"
//   double mousedown on same               → "wails:drag:doubleclick"
//   mousedown within edge/corner zones     → "wails:resize:<edge>"
//
// Protocol and zone sizes mirror Wails v3.0.0's runtime drag.ts.

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
  const inB = y < H - 1 && b < HANDLE + CORNER_EXTRA;
  // corners first
  if (inR && inB && r < HANDLE + CORNER_EXTRA && b < HANDLE + CORNER_EXTRA) return "se-resize";
  if (inL && inB) return "sw-resize";
  if (inL && inT) return "nw-resize";
  if (inT && inR) return "ne-resize";
  // then edges
  if (r < HANDLE && x < W - 1) return "e-resize";
  if (x < HANDLE) return "w-resize";
  if (y < HANDLE) return "n-resize";
  if (b < HANDLE && y < H - 1) return "s-resize";
  return "";
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
      if (edge) {
        invoke("wails:resize:" + edge);
        return;
      }
      const t = e.target instanceof Element ? e.target : null;
      if (!t) return;
      const drag =
        getComputedStyle(t).getPropertyValue("--wails-draggable").trim() ===
        "drag";
      if (!drag) return;
      e.preventDefault(); // no text selection while dragging the window
      invoke(e.detail >= 2 ? "wails:drag:doubleclick" : "wails:drag");
    },
    true,
  );
}
