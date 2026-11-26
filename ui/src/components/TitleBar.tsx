// Custom window chrome for the frameless native window (SPEC G12: the
// app owns its chrome). The server exposes /api/window/* only when the
// Wails shell is live; a 404 means browser/headless mode and the bar
// hides itself. Dragging + edge-resize are handled by the _wails
// message shim (windrag.ts); double-click on the bar maximises via the
// native wails:drag:doubleclick message.

import { useCallback, useEffect, useState } from "react";
import { windowAction, windowState } from "../api";

export function TitleBar() {
  const [shell, setShell] = useState<{ maximized: boolean } | null | undefined>(
    undefined,
  );

  const sync = useCallback(() => {
    windowState()
      .then(setShell)
      .catch(() => setShell(null));
  }, []);

  useEffect(() => {
    sync();
    // maximise/restore fires a window resize → keep the icon in sync
    window.addEventListener("resize", sync);
    return () => window.removeEventListener("resize", sync);
  }, [sync]);

  // Drive window-level CSS: .shell enables the rounded, shadowed app
  // frame in the transparent window; .max flattens it when maximised.
  useEffect(() => {
    const el = document.documentElement;
    el.classList.toggle("shell", !!shell);
    el.classList.toggle("max", !!shell?.maximized);
  }, [shell]);

  if (!shell) return null;
  const max = shell.maximized;

  return (
    <div className="titlebar shrink-0 flex items-center text-[12px] text-mute select-none">
      <span className="pl-3.5 pr-1 text-[15px] leading-none">⚡</span>
      <span className="font-semibold text-[13px] text-ink tracking-tight">
        Genisys
      </span>
      <div className="flex-1 h-full" />
      <button
        className="tb-btn"
        title="Minimize"
        onClick={() => void windowAction("minimize")}
      >
        <svg width="14" height="14" viewBox="0 0 16 16">
          <path d="M4 12h8" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
        </svg>
      </button>
      <button
        className="tb-btn"
        title={max ? "Restore" : "Maximize"}
        onClick={() => void windowAction("toggle-maximize")}
      >
        {max ? (
          <svg width="13" height="13" viewBox="0 0 16 16" fill="none">
            <rect x="3" y="5.5" width="7" height="7" rx="1.5" stroke="currentColor" strokeWidth="1.3" />
            <path d="M5.5 3.5h6a1.5 1.5 0 0 1 1.5 1.5v6" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
          </svg>
        ) : (
          <svg width="13" height="13" viewBox="0 0 16 16" fill="none">
            <rect x="3.5" y="3.5" width="9" height="9" rx="1.5" stroke="currentColor" strokeWidth="1.3" />
          </svg>
        )}
      </button>
      <button
        className="tb-btn tb-close"
        title="Close"
        onClick={() => void windowAction("close")}
      >
        <svg width="14" height="14" viewBox="0 0 16 16">
          <path d="M4.5 4.5l7 7m0-7l-7 7" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
        </svg>
      </button>
    </div>
  );
}
