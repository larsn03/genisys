// Three-pane shell (SPEC §5.1): SessionRail · MessageStream+Composer ·
// Files/Editor. Bionic-style dark chrome.

import { useEffect, useState } from "react";
import { useStore } from "./store";
import { MessageStream } from "./components/MessageStream";
import { Composer } from "./components/Composer";

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

  useEffect(() => {
    void refresh();
    const t = setInterval(() => void useStore.getState().refreshSessions(), 5000);
    return () => clearInterval(t);
  }, [refresh]);

  const sess = sessions.find((s) => s.id === current);

  return (
    <div className="h-full flex">
      {/* left rail */}
      <aside className="w-60 shrink-0 border-r border-line bg-panel/50 flex flex-col">
        <div className="px-4 py-3.5 flex items-center gap-2">
          <span className="text-lg">⚡</span>
          <span className="font-semibold tracking-tight">Genisys</span>
          <span
            className={`ml-auto w-2 h-2 rounded-full ${connected ? "bg-ok" : "bg-err"}`}
            title={connected ? "connected" : "reconnecting…"}
          />
        </div>
        <button
          onClick={() => void create()}
          className="mx-3 mb-2 px-3 py-2 rounded-xl bg-accent/90 hover:bg-accent text-white text-[13px] font-medium"
        >
          + New session
        </button>
        <div className="flex-1 overflow-y-auto px-2 pb-3 flex flex-col gap-0.5">
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

      {/* center */}
      <main className="flex-1 min-w-0 flex flex-col relative">
        {current ? (
          <>
            <header className="px-6 py-3 border-b border-line flex items-center gap-3 text-xs text-mute">
              <span className="text-ink text-sm font-medium truncate">
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
      {current && <RightPanel />}

      {error && (
        <div
          className="fixed bottom-4 right-4 max-w-md border border-err/40 bg-panel rounded-xl px-4 py-3 text-sm text-err shadow-xl"
          onClick={() => useStore.setState({ error: null })}
        >
          {error} ✕
        </div>
      )}
    </div>
  );
}

function RightPanel() {
  const files = useStore((s) => s.files);
  const refreshFiles = useStore((s) => s.refreshFiles);
  const openFile = useStore((s) => s.openFile);
  const filePath = useStore((s) => s.filePath);
  const fileContent = useStore((s) => s.fileContent);
  const saveFile = useStore((s) => s.saveFile);
  const [draft, setDraft] = useState("");
  const [tab, setTab] = useState<"files" | "editor">("files");

  useEffect(() => setDraft(fileContent), [fileContent]);

  return (
    <aside className="w-96 shrink-0 border-l border-line bg-panel/50 flex flex-col">
      <div className="flex border-b border-line text-[13px]">
        {(["files", "editor"] as const).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`px-4 py-2.5 capitalize ${
              tab === t ? "text-ink border-b-2 border-accent" : "text-faint hover:text-mute"
            }`}
          >
            {t}
            {t === "editor" && filePath && (
              <span className="ml-1.5 text-faint font-mono text-xs">
                {filePath.split("/").pop()}
              </span>
            )}
          </button>
        ))}
        <button
          onClick={() => void refreshFiles()}
          className="ml-auto px-3 text-faint hover:text-ink"
          title="Refresh"
        >
          ⟳
        </button>
      </div>
      {tab === "files" ? (
        <div className="flex-1 overflow-y-auto p-2 font-mono text-[13px]">
          {files.map((f) => (
            <div
              key={f.name}
              onClick={() =>
                f.is_dir ? void refreshFiles(f.name) : void (openFile(f.name), setTab("editor"))
              }
              className="flex items-center gap-2 px-2 py-1.5 rounded-lg hover:bg-panel2 cursor-pointer"
            >
              <span className={f.is_dir ? "text-accent2" : "text-faint"}>
                {f.is_dir ? "▸" : "·"}
              </span>
              <span className="truncate flex-1">{f.name}</span>
              {!f.is_dir && <span className="text-faint text-xs">{f.size}B</span>}
            </div>
          ))}
          {!files.length && <div className="text-faint text-xs p-2">Empty workspace.</div>}
        </div>
      ) : (
        <div className="flex-1 flex flex-col">
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            className="flex-1 bg-transparent outline-none resize-none p-3 font-mono text-[12.5px] leading-relaxed"
            spellCheck={false}
          />
          <div className="p-2 border-t border-line flex gap-2">
            <button
              onClick={() => void saveFile(draft)}
              disabled={draft === fileContent}
              className="px-3 py-1.5 rounded-lg bg-accent text-white text-[13px] disabled:opacity-40"
            >
              Save
            </button>
            {draft !== fileContent && <span className="text-warn text-xs self-center">modified</span>}
          </div>
        </div>
      )}
    </aside>
  );
}
