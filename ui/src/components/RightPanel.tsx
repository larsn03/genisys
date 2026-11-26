// Right panel (SPEC §5.4): tabstrip Files · Editor · History · Browser.
// FilesTree carries git badges + context menu; EditorPane is Monaco (lazy);
// History lists shadow checkpoints with per-step rollback; Browser is the
// in-app preview pane (localhost + allow-list, focus-or-open semantics).

import { Suspense, lazy, useEffect, useState } from "react";
import { api } from "../api";
import { useStore } from "../store";
import { Chev } from "./Chev";

const EditorPane = lazy(() =>
  import("./EditorPane").then((m) => ({ default: m.EditorPane })),
);

type Tab = "files" | "editor" | "history" | "browser";

export function RightPanel() {
  const [tab, setTab] = useState<Tab>("files");
  const filePath = useStore((s) => s.filePath);
  const checkpoints = useStore((s) => s.checkpoints);
  const width = useStore((s) => s.layout.right);
  const browser = useStore((s) => s.browser);

  useEffect(() => {
    if (filePath) setTab("editor");
  }, [filePath]);

  // focus-or-open (SPEC §9.6): a ui_open_url event switches to the
  // browser tab unless we are still inside the initial replay window
  // (selectedAt), which would steal focus on every session reload.
  useEffect(() => {
    if (!browser) return;
    if (Date.now() - useStore.getState().selectedAt > 1500) setTab("browser");
  }, [browser?.nonce]);

  return (
    <aside className="shrink-0 border-l border-line bg-panel/50 flex flex-col" style={{ width }}>
      <div className="flex border-b border-line text-[13px]">
        {(["files", "editor", "history", "browser"] as const).map((t) => (
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
            {t === "history" && checkpoints.length > 0 && (
              <span className="ml-1.5 text-faint text-xs">{checkpoints.length}</span>
            )}
            {t === "browser" && browser && (
              <span className="ml-1.5 text-accent text-xs">●</span>
            )}
          </button>
        ))}
        <button
          onClick={() => {
            void useStore.getState().refreshFiles(useStore.getState().dirPath);
            void useStore.getState().refreshGit();
            void useStore.getState().refreshCheckpoints();
          }}
          className="ml-auto px-3 text-faint hover:text-ink"
          title="Refresh"
        >
          ⟳
        </button>
        <button
          onClick={() => useStore.getState().setLayout({ rightOpen: false })}
          className="px-3 text-faint hover:text-ink border-l border-line self-stretch"
          title="Collapse workspace panel"
        >
          <Chev dir="right" />
        </button>
      </div>
      {tab === "files" && <FilesTab />}
      {tab === "editor" && (
        <Suspense fallback={<div className="text-faint text-xs p-3">loading editor…</div>}>
          <EditorPane />
        </Suspense>
      )}
      {tab === "history" && <HistoryTab />}
      {tab === "browser" && <BrowserPane />}
      <FileHistoryModal />
    </aside>
  );
}

function FilesTab() {
  const files = useStore((s) => s.files);
  const dirPath = useStore((s) => s.dirPath);
  const git = useStore((s) => s.git);
  const refreshFiles = useStore((s) => s.refreshFiles);
  const openFile = useStore((s) => s.openFile);
  const [menu, setMenu] = useState<{ x: number; y: number; path: string } | null>(null);

  const badge = (name: string) => {
    if (!git?.repo) return null;
    const full = dirPath ? `${dirPath}/${name}` : name;
    const code = git.files?.[full] ?? git.files?.[name];
    if (!code) return null;
    const color = code.includes("?")
      ? "text-ok"
      : code.includes("D")
        ? "text-err"
        : code.includes("A")
          ? "text-accent2"
          : "text-warn";
    return <span className={`font-mono text-[10px] ${color}`}>{code}</span>;
  };

  return (
    <div className="flex-1 flex flex-col min-h-0">
      <div className="px-2.5 py-1.5 flex items-center gap-1.5 text-[11px] text-faint font-mono border-b border-line/50">
        {git?.repo && <span className="text-accent2">⎇ {git.branch || "main"}</span>}
        <button
          onClick={() => void refreshFiles(dirPath.includes("/") ? dirPath.split("/").slice(0, -1).join("/") : "")}
          className={dirPath ? "hover:text-ink" : "opacity-30"}
          disabled={!dirPath}
        >
          …/
        </button>
        <span className="truncate">{dirPath || "workspace root"}</span>
      </div>
      <div className="flex-1 overflow-y-auto p-2 font-mono text-[13px] relative">
        {files.map((f) => {
          const full = dirPath ? `${dirPath}/${f.name}` : f.name;
          return (
            <div
              key={full}
              onClick={() => (f.is_dir ? void refreshFiles(full) : void openFile(full))}
              onDoubleClick={() => !f.is_dir && useStore.getState().openPreview(full)}
              onContextMenu={(e) => {
                e.preventDefault();
                setMenu({ x: e.clientX, y: e.clientY, path: full });
              }}
              className="flex items-center gap-2 px-2 py-1.5 rounded-lg hover:bg-panel2 cursor-pointer"
            >
              <span className={f.is_dir ? "text-accent2" : "text-faint"}>
                {f.is_dir ? "▸" : "·"}
              </span>
              <span className="truncate flex-1">{f.name}</span>
              {badge(f.name)}
              {!f.is_dir && /\.(md|txt|pdf|png|jpe?g|gif|pptx|docx|xlsx|csv|json)$/i.test(f.name) && (
                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    useStore.getState().openPreview(full);
                  }}
                  className="text-faint hover:text-ink text-[11px]"
                  title="Preview"
                >
                  👁
                </button>
              )}
              {!f.is_dir && <span className="text-faint text-[10px]">{f.size}B</span>}
            </div>
          );
        })}
        {!files.length && <div className="text-faint text-xs p-2">Empty workspace.</div>}
        {menu && (
          <div
            className="fixed z-50 bg-panel border border-line rounded-lg shadow-xl py-1 text-xs min-w-40"
            style={{ left: menu.x, top: menu.y }}
            onMouseLeave={() => setMenu(null)}
          >
            <button
              className="w-full text-left px-3 py-1.5 hover:bg-panel2"
              onClick={() => {
                void openFile(menu.path);
                setMenu(null);
              }}
            >
              Open in editor
            </button>
            <button
              className="w-full text-left px-3 py-1.5 hover:bg-panel2"
              onClick={() => {
                void navigator.clipboard?.writeText(menu.path);
                setMenu(null);
              }}
            >
              Copy path
            </button>
            <button
              className="w-full text-left px-3 py-1.5 hover:bg-panel2"
              onClick={() => {
                useStore.getState().openHistory(menu.path);
                setMenu(null);
              }}
            >
              History & restore…
            </button>
            <button
              className="w-full text-left px-3 py-1.5 hover:bg-panel2"
              onClick={() => {
                void useStore.getState().refreshFiles(dirPath);
                setMenu(null);
              }}
            >
              Refresh
            </button>
          </div>
        )}
      </div>
    </div>
  );
}

function HistoryTab() {
  const checkpoints = useStore((s) => s.checkpoints);
  const revertStep = useStore((s) => s.revertStep);
  const [confirm, setConfirm] = useState<string | null>(null);
  return (
    <div className="flex-1 overflow-y-auto p-2 flex flex-col gap-1">
      {checkpoints.map((c) => (
        <div
          key={c.id}
          className="group flex items-center gap-2 px-2.5 py-2 rounded-lg hover:bg-panel2 text-[12.5px]"
        >
          <span className="text-faint font-mono text-[10px] shrink-0">
            {c.created_at.slice(11, 19)}
          </span>
          <span className="truncate flex-1" title={c.label}>
            {c.step_id ? <span className="font-mono text-accent2">{c.step_id}</span> : null}{" "}
            {c.label}
          </span>
          {c.step_id &&
            (confirm === c.step_id ? (
              <span className="flex gap-1">
                <button
                  onClick={() => {
                    void revertStep(c.step_id);
                    setConfirm(null);
                  }}
                  className="px-2 py-0.5 rounded bg-err/80 text-white text-[11px]"
                >
                  Confirm
                </button>
                <button
                  onClick={() => setConfirm(null)}
                  className="px-2 py-0.5 rounded border border-line text-faint text-[11px]"
                >
                  No
                </button>
              </span>
            ) : (
              <button
                onClick={() => setConfirm(c.step_id)}
                className="opacity-0 group-hover:opacity-100 px-2 py-0.5 rounded border border-line text-warn text-[11px] hover:bg-warn/10"
                title={`Roll workspace back to before ${c.step_id}`}
              >
                ↺ Revert
              </button>
            ))}
        </div>
      ))}
      {!checkpoints.length && (
        <div className="text-faint text-xs p-2">
          No checkpoints yet — they are created automatically whenever a file changes.
        </div>
      )}
    </div>
  );
}

function BrowserPane() {
  const browser = useStore((s) => s.browser);
  const origins = useStore((s) => s.browserOrigins);
  const [addr, setAddr] = useState("");

  useEffect(() => {
    if (browser) setAddr(browser.url);
  }, [browser?.nonce]);

  if (!browser) {
    return (
      <div className="flex-1 flex flex-col items-center justify-center text-faint text-xs gap-2 p-6 text-center">
        <div className="text-2xl">🌐</div>
        <div>
          The in-app browser shows localhost dev servers and allow-listed
          sites. Ask the agent to `open_url_in_app_browser`.
        </div>
      </div>
    );
  }

  let allowed = false;
  let parsed: URL | null = null;
  try {
    parsed = new URL(browser.url);
    allowed =
      ["http:", "https:", "file:"].includes(parsed.protocol) &&
      (parsed.hostname === "localhost" ||
        parsed.hostname === "127.0.0.1" ||
        origins.includes(parsed.origin));
  } catch {
    /* fallthrough */
  }

  const go = () => {
    try {
      const u = new URL(addr);
      useStore.getState().openBrowser(u.toString());
    } catch {
      /* keep old */
    }
  };

  return (
    <div className="flex-1 flex flex-col min-h-0">
      <form
        className="flex gap-1.5 p-2 border-b border-line"
        onSubmit={(e) => {
          e.preventDefault();
          go();
        }}
      >
        <input
          value={addr}
          onChange={(e) => setAddr(e.target.value)}
          className="flex-1 bg-field border border-line rounded px-2 py-1 text-xs font-mono text-ink"
          placeholder="http://localhost:…"
          spellCheck={false}
        />
        <button className="text-xs px-2 py-1 rounded bg-field border border-line text-mute hover:text-ink">
          go
        </button>
        <a
          href={browser.url}
          target="_blank"
          rel="noreferrer"
          className="text-xs px-2 py-1 rounded bg-field border border-line text-mute hover:text-ink"
          title="Open externally"
        >
          ↗
        </a>
      </form>
      {allowed ? (
        <iframe
          key={`${browser.nonce}`}
          src={browser.url}
          title="in-app browser"
          className="flex-1 w-full bg-white"
          sandbox="allow-scripts allow-forms allow-same-origin allow-popups"
        />
      ) : (
        <div className="flex-1 p-4 text-xs text-faint">
          {parsed ? (
            <>
              <span className="text-warn">
                {parsed.origin} is not localhost and not in
                browser.allowed_origins.
              </span>{" "}
              Add it via Settings → Browser, or open it{" "}
              <a
                href={browser.url}
                target="_blank"
                rel="noreferrer"
                className="text-accent underline"
              >
                externally
              </a>
              .
            </>
          ) : (
            "invalid url"
          )}
        </div>
      )}
    </div>
  );
}

// SPEC §11: per-file snapshot timeline with preview + restore; 409 from
// user edits gets an explicit force confirmation.
function FileHistoryModal() {
  const path = useStore((s) => s.historyPath);
  const close = useStore((s) => s.closeHistory);
  const current = useStore((s) => s.current);
  const [hist, setHist] = useState<{ sha: string; label: string; when: string }[]>([]);
  const [sel, setSel] = useState("");
  const [preview, setPreview] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => {
    if (!path || !current) return;
    setHist([]);
    setSel("");
    setPreview("");
    setErr("");
    void api
      .fileHistory(current, path)
      .then((h) => {
        setHist(h);
        if (h[0]) {
          setSel(h[0].sha);
          void fetch(api.fileAtURL(current, path, h[0].sha))
            .then((r) => r.text())
            .then(setPreview)
            .catch(() => setPreview("(binary or unreadable)"));
        }
      })
      .catch((e) => setErr(String(e)));
  }, [path, current]);

  if (!path) return null;

  const pick = (sha: string) => {
    setSel(sha);
    setPreview("…");
    void fetch(api.fileAtURL(current!, path, sha))
      .then((r) => (r.ok ? r.text() : Promise.reject(new RegExp("gone"))))
      .then(setPreview)
      .catch(() => setPreview("(binary or unreadable)"));
  };

  const restore = async (force: boolean) => {
    if (!current || !sel) return;
    setBusy(true);
    setErr("");
    try {
      await api.restoreFile(current, path, sel, force);
      close();
      void useStore.getState().refreshFiles(useStore.getState().dirPath);
    } catch (e) {
      const msg = String(e);
      if (!force && msg.includes("user edits")) {
        if (confirm("User edits since the last checkpoint would be overwritten. Restore anyway?"))
          await restore(true);
      } else {
        setErr(msg);
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="fixed inset-0 z-40 bg-black/40 flex items-center justify-center p-6" onClick={close}>
      <div
        className="w-full max-w-3xl max-h-[80vh] bg-panel border border-line rounded-2xl shadow-2xl flex flex-col overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 px-4 py-3 border-b border-line">
          <span className="text-sm text-ink">History —</span>
          <span className="font-mono text-xs text-mute truncate">{path}</span>
          <button onClick={close} className="ml-auto text-faint hover:text-ink text-sm">
            ✕
          </button>
        </div>
        <div className="flex flex-1 min-h-0">
          <div className="w-72 shrink-0 border-r border-line overflow-y-auto py-1 text-[12px]">
            {hist.length === 0 && !err && <div className="p-3 text-faint">no snapshots yet</div>}
            {hist.map((c) => (
              <button
                key={c.sha}
                onClick={() => pick(c.sha)}
                className={`block w-full text-left px-3 py-2 ${
                  sel === c.sha ? "bg-panel2 text-ink" : "text-mute hover:bg-panel2/60"
                }`}
              >
                <div className="truncate">{c.label.split("·")[0] || c.label}</div>
                <div className="text-[10px] text-faint">
                  {new Date(c.when).toLocaleString()} · {c.sha.slice(0, 7)}
                </div>
              </button>
            ))}
          </div>
          <pre className="flex-1 overflow-auto p-3 text-[11px] font-mono text-mute whitespace-pre-wrap">
            {preview}
          </pre>
        </div>
        {(err || busy) && <div className="px-4 py-2 text-xs text-warn">{err || "restoring…"}</div>}
        <div className="px-4 py-3 border-t border-line flex justify-end">
          <button
            disabled={!sel || busy}
            onClick={() => void restore(false)}
            className="text-xs px-3 py-1.5 rounded-lg border border-accent text-accent hover:bg-accent/10 disabled:opacity-40"
          >
            ↺ Restore this snapshot
          </button>
        </div>
      </div>
    </div>
  );
}
