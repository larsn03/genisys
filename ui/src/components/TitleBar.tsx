// Custom window chrome for the frameless native window (SPEC G12: the
// app owns its chrome). Renders only when the Wails shell service is
// present (i.e. inside `genisys` proper); browser tabs skip it entirely.
// Dragging: Wails runtime honours `--wails-draggable: drag` (see CSS);
// double-click maximises; edge-resize is handled by the runtime itself.

import { useEffect, useState } from "react";
import { findShell, shellCall, type ShellAPI } from "../shell";

export function TitleBar() {
  const [shell] = useState<ShellAPI | null>(() => findShell());
  const [max, setMax] = useState(false);

  useEffect(() => {
    if (!shell) return;
    const sync = () =>
      shellCall(shell.IsMaximized()).then((v) => setMax(v === true));
    void sync();
    window.addEventListener("resize", sync);
    return () => window.removeEventListener("resize", sync);
  }, [shell]);

  if (!shell) return null;

  const act = (fn: () => unknown) => (e: React.MouseEvent) => {
    e.stopPropagation();
    void shellCall(fn());
  };

  return (
    <div
      className="titlebar shrink-0 flex items-center text-[12px] text-mute select-none"
      onDoubleClick={() => void shellCall(shell.ToggleMaximize())}
    >
      <span className="pl-3.5 pr-1 text-[15px] leading-none">⚡</span>
      <span className="font-semibold text-[13px] text-ink tracking-tight">
        Genisys
      </span>
      <div className="flex-1 h-full" />
      <button className="tb-btn" title="Minimize" onClick={act(shell.Minimize)}>
        <svg width="14" height="14" viewBox="0 0 16 16">
          <path d="M4 12h8" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
        </svg>
      </button>
      <button
        className="tb-btn"
        title={max ? "Restore" : "Maximize"}
        onClick={act(shell.ToggleMaximize)}
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
      <button className="tb-btn tb-close" title="Close" onClick={act(shell.Close)}>
        <svg width="14" height="14" viewBox="0 0 16 16">
          <path d="M4.5 4.5l7 7m0-7l-7 7" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
        </svg>
      </button>
    </div>
  );
}
