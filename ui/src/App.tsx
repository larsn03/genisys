// Three-pane shell (SPEC §5.1): SessionRail · MessageStream+Composer ·
// Files/Editor/History. Resizable + collapsible panes persisted to
// localStorage (P3-9). Bionic-style dark chrome.

import { useEffect, useRef, useState } from "react";
import { useStore } from "./store";
import { MessageStream } from "./components/MessageStream";
import { Composer } from "./components/Composer";
import { RightPanel } from "./components/RightPanel";

export default function App() {
  const sessions = useStore((s) => s.sessions);
  const current = useStore((s) => s.current);
  const select = useStore((s) => s.select);
  const refresh = useStore((s) => s.refreshSessions);
  const create = useStore((s) => s.createSession);
  const archive = useStore((s) => s.archive);
  const connected = useStore((s) => s.connected);
  const running = useStore((s) => s.running);
  const error = useStore((s) => s.error);
  const layout = useStore((s) => s.layout);
  const setLayout = useStore((s) => s.setLayout);
  const loadModels = useStore((s) => s.loadModels);

  useEffect(() => {
    void loadModels();
    void refresh();
    const t = setInterval(() => void useStore.getState().refreshSessions(), 5000);
    return () => clearInterval(t);
  }, [refresh, loadModels]);

  const sess = sessions.find((s) => s.id === current);

  return (
    <div className="h-full flex min-w-0">
      {/* left rail */}
      <aside
        className="shrink-0 border-r border-line bg-panel/50 flex flex-col overflow-hidden"
        style={{ width: layout.leftOpen ? layout.left : 0, borderWidth: layout.leftOpen ? 1 : 0 }}
      >
        <div className="px-4 py-3.5 flex items-center gap-2 min-w-60">
          <span className="text-lg">⚡</span>
          <span className="font-semibold tracking-tight">Genisys</span>
          <span
            className={`ml-auto w-2 h-2 rounded-full ${connected ? "bg-ok" : "bg-err"}`}
            title={connected ? "connected" : "reconnecting…"}
          />
        </div>
        <button
          onClick={() => void create()}
          className="mx-3 mb-2 px-3 py-2 rounded-xl bg-accent/90 hover:bg-accent text-white text-[13px] font-medium min-w-54"
        >
          + New session
        </button>
        <div className="flex-1 overflow-y-auto px-2 pb-3 flex flex-col gap-0.5 min-w-54">
          {sessions.map((s) => (
            <div
              key={s.id}
              onClick={() => void select(s.id)}
              className={`group flex items-center gap-2 px-2.5 py-2 rounded-lg cursor-pointer text-[13px] ${
                s.id === current ? "bg-panel2 text-ink" : "text-mute hover:bg-panel2/60"
              }`}
            >
              <span
                className={`w-1.5 h-1.5 rounded-full shrink-0 ${
                  s.id === current && running
                    ? "bg-accent2 animate-pulse"
                    : s.pinned
                      ? "bg-warn"
                      : "bg-line"
                }`}
              />
              <span className="truncate flex-1">{s.title}</span>
              <button
                onClick={(e) => {
                  e.stopPropagation();
                  void archive(s.id);
                }}
                className="opacity-0 group-hover:opacity-100 text-faint hover:text-err text-xs"
                title="Archive session"
              >
                ✕
              </button>
            </div>
          ))}
          {!sessions.length && (
            <div className="text-xs text-faint px-2.5 py-2">No sessions yet.</div>
          )}
        </div>
      </aside>

      <DragEdge
        onDrag={(dx) =>
          setLayout({ left: Math.min(420, Math.max(160, useStore.getState().layout.left + dx)) })
        }
        hidden={!layout.leftOpen}
        label="‹"
      />

      {/* center */}
      <main className="flex-1 min-w-0 flex flex-col relative">
        <button
          onClick={() => setLayout({ leftOpen: !layout.leftOpen })}
          className="absolute top-3 left-2 z-10 text-faint hover:text-ink text-xs px-1"
          title={`${layout.leftOpen ? "Collapse" : "Expand"} sessions rail`}
        >
          {layout.leftOpen ? "⟨" : "⟩"} sessions
        </button>
        {current ? (
          <>
            <header className="px-6 py-3 border-b border-line flex items-center gap-3 text-xs text-mute">
              <span className="text-ink text-sm font-medium truncate pl-12">
                {sess?.title ?? "…"}
              </span>
              <span className="font-mono">{sess?.model_ref}</span>
              {running && <span className="text-accent2 animate-pulse">working</span>}
            </header>
            <MessageStream />
            <Composer />
          </>
        ) : (
          <div className="flex-1 grid place-items-center text-mute">
            <div className="text-center">
              <div className="text-3xl mb-2">⚡</div>
              <div className="text-sm">
                Create a session and ask Genisys to build, fix, or explain something.
              </div>
            </div>
          </div>
        )}
      </main>

      {/* right panel */}
      {current && layout.rightOpen && (
        <>
          <DragEdge
            flip
            onDrag={(dx) =>
              setLayout({
                right: Math.min(720, Math.max(280, useStore.getState().layout.right - dx)),
              })
            }
          />
          <RightPanel />
        </>
      )}
      {current && (
        <button
          onClick={() => setLayout({ rightOpen: !layout.rightOpen })}
          className="absolute top-3 right-2 z-10 text-faint hover:text-ink text-xs px-1"
          title={`${layout.rightOpen ? "Collapse" : "Open"} workspace panel`}
        >
          {layout.rightOpen ? "⟩" : "⟨"} files
        </button>
      )}

      {error && (
        <div
          className="fixed bottom-4 right-4 max-w-md border border-err/40 bg-panel rounded-xl px-4 py-3 text-sm text-err shadow-xl cursor-pointer z-50"
          onClick={() => useStore.setState({ error: null })}
        >
          {error} ✕
        </div>
      )}
    </div>
  );
}

function DragEdge({
  onDrag,
  flip,
  hidden,
  label,
}: {
  onDrag: (dx: number) => void;
  flip?: boolean;
  hidden?: boolean;
  label?: string;
}) {
  const [active, setActive] = useState(false);
  const last = useRef(0);
  if (hidden) return null;
  return (
    <div
      onPointerDown={(e) => {
        e.preventDefault();
        (e.target as HTMLElement).setPointerCapture(e.pointerId);
        setActive(true);
        last.current = e.clientX;
      }}
      onPointerMove={(e) => {
        if (!active) return;
        const dx = e.clientX - last.current;
        last.current = e.clientX;
        if (dx) onDrag(flip ? -dx : dx);
      }}
      onPointerUp={() => setActive(false)}
      className={`w-1 shrink-0 cursor-col-resize hover:bg-accent/40 transition-colors ${
        active ? "bg-accent/60" : ""
      }`}
      title={`drag to resize${label ? ` (${label})` : ""}`}
    />
  );
}
