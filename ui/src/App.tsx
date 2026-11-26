// Three-pane shell (SPEC §5.1): SessionRail · MessageStream+Composer ·
// Files/Editor/History. Resizable + collapsible panes persisted to
// localStorage (P3-9). Bionic-style dark chrome.

import { useEffect, useMemo, useRef, useState } from "react";
import { useStore } from "./store";
import { MessageStream } from "./components/MessageStream";
import { Composer } from "./components/Composer";
import { RightPanel } from "./components/RightPanel";
import { PreviewOverlay } from "./components/PreviewOverlay";
import { SettingsModal } from "./components/SettingsModal";
import { Onboarding } from "./components/Onboarding";
import { Chev } from "./components/Chev";

export default function App() {
  const sessions = useStore((s) => s.sessions);
  const current = useStore((s) => s.current);
  const refresh = useStore((s) => s.refreshSessions);
  const create = useStore((s) => s.createSession);
  const projects = useStore((s) => s.projects);
  const refreshProjects = useStore((s) => s.refreshProjects);
  const connected = useStore((s) => s.connected);
  const running = useStore((s) => s.running);
  const error = useStore((s) => s.error);
  const layout = useStore((s) => s.layout);
  const setLayout = useStore((s) => s.setLayout);
  const loadModels = useStore((s) => s.loadModels);
  const applyTheme = useStore((s) => s.applyTheme);
  const models = useStore((s) => s.models);

  useEffect(() => {
    applyTheme();
    const mq = matchMedia("(prefers-color-scheme: light)");
    const onMQ = () => applyTheme();
    mq.addEventListener("change", onMQ);
    return () => mq.removeEventListener("change", onMQ);
  }, [applyTheme]);

  useEffect(() => {
    void loadModels();
    void useStore.getState().applyOpenTarget();
    void refresh();
    void refreshProjects();
    const t = setInterval(() => void useStore.getState().refreshSessions(), 5000);
    return () => clearInterval(t);
  }, [refresh, refreshProjects, loadModels]);

  const byProject = useMemo(() => {
    const map = new Map<string, typeof sessions>();
    for (const s of sessions) {
      const k = s.project_id ?? "";
      if (!map.has(k)) map.set(k, []);
      map.get(k)!.push(s);
    }
    return map;
  }, [sessions]);

  const sess = sessions.find((s) => s.id === current);
  const projName = projects.find((p) => p.id === sess?.project_id)?.name;

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
          <button
            onClick={() => setLayout({ leftOpen: false })}
            className="text-faint hover:text-ink p-1 rounded-md hover:bg-panel2 -mr-1"
            title="Collapse sessions rail"
          >
            <Chev dir="left" />
          </button>
        </div>
        <button
          onClick={() => void create()}
          className="mx-3 mb-2 px-3 py-2 rounded-xl bg-accent/90 hover:bg-accent text-white text-[13px] font-medium min-w-54"
        >
          + New session
        </button>
        <div className="flex-1 overflow-y-auto px-2 pb-3 flex flex-col gap-0.5 min-w-54">
          {projects.map((pr) => {
            const list = byProject.get(pr.id) ?? [];
            return (
              <div key={pr.id} className="mb-1">
                <div className="group flex items-center gap-1.5 px-2 pt-2 pb-1 text-[11px] uppercase tracking-wide text-faint">
                  <span className="truncate flex-1" title={pr.workspace_path}>
                    ⌗ {pr.name}
                  </span>
                  <button
                    onClick={() => void create(pr.id)}
                    className="opacity-0 group-hover:opacity-100 hover:text-ink"
                    title={`New session in ${pr.name}`}
                  >
                    +
                  </button>
                </div>
                {list.map((s) => (
                  <SessionRowView key={s.id} s={s} current={current} running={running} />
                ))}
              </div>
            );
          })}
          {(byProject.get("")?.length ?? 0) > 0 && (
            <div className="mb-1">
              <div className="px-2 pt-2 pb-1 text-[11px] uppercase tracking-wide text-faint">
                💬 Chats
              </div>
              {(byProject.get("") ?? []).map((s) => (
                <SessionRowView key={s.id} s={s} current={current} running={running} />
              ))}
            </div>
          )}
          {!sessions.length && (
            <div className="text-xs text-faint px-2.5 py-2">No sessions yet.</div>
          )}
          <NewProjectForm />
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
        {!layout.leftOpen && (
          <button
            onClick={() => setLayout({ leftOpen: true })}
            className="absolute top-2.5 left-2.5 z-10 text-faint hover:text-ink p-1.5 rounded-md hover:bg-panel2 border border-line bg-panel/70"
            title="Expand sessions rail"
          >
            <Chev dir="right" />
          </button>
        )}
        {current ? (
          <>
            <header className="h-11 shrink-0 px-4 border-b border-line flex items-center gap-2.5 text-xs text-mute">
              <span className="text-ink text-sm font-medium truncate">
                {sess?.title ?? "…"}
              </span>
              {projName && (
                <span className="text-faint text-[11px] uppercase tracking-wider truncate">
                  · {projName}
                </span>
              )}
              <span className="font-mono text-[11px] border border-line rounded-md px-1.5 py-0.5 text-mute whitespace-nowrap">
                {sess?.model_ref}
              </span>
              {running && <span className="text-accent2 animate-pulse">working</span>}
              <button
                onClick={() => useStore.getState().setSettingsOpen(true)}
                className="ml-auto text-faint hover:text-ink text-sm p-1.5 rounded-md hover:bg-panel2"
                title="Settings"
              >
                ⚙
              </button>
              {!layout.rightOpen && (
                <button
                  onClick={() => setLayout({ rightOpen: true })}
                  className="text-faint hover:text-ink p-1.5 rounded-md hover:bg-panel2 border border-line"
                  title="Open workspace panel"
                >
                  <Chev dir="left" />
                </button>
              )}
            </header>
            <MessageStream />
            <Composer />
          </>
        ) : (
          <div className="flex-1 grid place-items-center text-mute">
            {models.length === 0 ? (
              <Onboarding />
            ) : (
              <div className="text-center max-w-md">
                <div className="mx-auto w-14 h-14 rounded-2xl bg-accent/15 border border-accent/25 grid place-items-center text-[26px]">
                  ⚡
                </div>
                <div className="mt-4 text-[15px] font-medium text-ink">Welcome to Genisys</div>
                <div className="mt-1.5 text-[13px] leading-relaxed">
                  Create a session and ask Genisys to build, fix, or explain
                  something — or open a folder with{" "}
                  <code className="font-mono text-[12px] bg-panel2 rounded px-1.5 py-0.5">
                    genisys &lt;dir&gt;
                  </code>
                  .
                </div>
              </div>
            )}
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
      <PreviewOverlay />
      <SettingsModal />

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

// finished-in-background dot (SPEC §5.2): events landed after our last visit
function isUnread(s: import("./types").SessionRow, current: string | null): boolean {
  if (s.id === current || s.status === "running" || s.status === "archived") return false;
  let seen = "";
  try {
    seen = localStorage.getItem(`gs_seen_${s.id}`) ?? "";
  } catch {
    return false;
  }
  if (!seen) return true; // never opened since boot
  return s.updated_at > seen;
}

function SessionRowView({
  s,
  current,
  running,
}: {
  s: import("./types").SessionRow;
  current: string | null;
  running: boolean;
}) {
  const select = useStore((st) => st.select);
  const fork = useStore((st) => st.fork);
  const rename = useStore((st) => st.rename);
  const togglePin = useStore((st) => st.togglePin);
  const hardDelete = useStore((st) => st.hardDelete);
  const archive = useStore((st) => st.archive);
  const [menu, setMenu] = useState(false);
  const [naming, setNaming] = useState(false);
  const [title, setTitle] = useState(s.title);
  const unread = isUnread(s, current);

  const act = (fn: () => void) => (e: React.MouseEvent) => {
    e.stopPropagation();
    setMenu(false);
    fn();
  };

  return (
    <div
      onClick={() => !naming && void select(s.id)}
      onContextMenu={(e) => {
        e.preventDefault();
        setMenu(true);
      }}
      className="relative group flex items-center gap-2 px-2.5 py-2 rounded-lg cursor-pointer text-[13px]"
    >
      {naming ? (
        <input
          autoFocus
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          onClick={(e) => e.stopPropagation()}
          onBlur={() => {
            setNaming(false);
            if (title.trim() && title !== s.title) void rename(s.id, title.trim());
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") (e.target as HTMLInputElement).blur();
            if (e.key === "Escape") {
              setTitle(s.title);
              setNaming(false);
            }
          }}
          className="flex-1 bg-transparent outline-none border-b border-accent text-[13px] min-w-0"
        />
      ) : (
        <>
          <span
            className={`w-1.5 h-1.5 rounded-full shrink-0 ${
              s.id === current && running
                ? "bg-accent2 animate-pulse"
                : unread
                  ? "bg-accent"
                  : s.pinned
                    ? "bg-warn"
                    : "bg-line"
            }`}
            title={unread ? "agent finished while you were away" : ""}
          />
          <span className={`truncate flex-1 ${unread ? "text-ink font-medium" : ""}`}>{s.title}</span>
        </>
      )}
      <button
        onClick={(e) => {
          e.stopPropagation();
          setMenu((m) => !m);
        }}
        className="opacity-0 group-hover:opacity-100 text-faint hover:text-ink text-xs px-1"
        title="Session menu"
      >
        ⋯
      </button>
      {menu && (
        <>
          <div
            className="fixed inset-0 z-20"
            onClick={(e) => {
              e.stopPropagation();
              setMenu(false);
            }}
          />
          <div
            className="absolute right-2 top-8 z-30 w-40 bg-panel border border-line rounded-xl shadow-2xl py-1 text-[12px]"
            onClick={(e) => e.stopPropagation()}
          >
          <button className="menu-item" onClick={act(() => togglePin(s.id))}>
            {s.pinned ? "Unpin" : "Pin"}
          </button>
          <button
            className="menu-item"
            onClick={act(() => {
              setTitle(s.title);
              setNaming(true);
            })}
          >
            Rename
          </button>
          <button className="menu-item" onClick={act(() => void fork(s.id))}>
            Fork
          </button>
          <button className="menu-item" onClick={act(() => void archive(s.id))}>
            Archive
          </button>
          <div className="menu-sep" />
          <button
            className="menu-item text-err"
            onClick={act(() => {
              if (confirm(`Delete "${s.title}" permanently?`)) void hardDelete(s.id);
            })}
          >
            Delete
          </button>
          </div>
        </>
      )}
    </div>
  );
}

function NewProjectForm() {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [path, setPath] = useState("");
  const createProject = useStore((s) => s.createProject);
  if (!open)
    return (
      <button
        onClick={() => setOpen(true)}
        className="mt-2 mx-1 px-2 py-1.5 text-[12px] text-faint hover:text-mute text-left rounded-lg hover:bg-panel2/60"
      >
        + New project
      </button>
    );
  return (
    <form
      className="mt-2 mx-1 p-2 rounded-xl border border-line bg-panel2/40 flex flex-col gap-1.5"
      onSubmit={(e) => {
        e.preventDefault();
        if (name.trim() && path.trim()) void createProject(name.trim(), path.trim());
        setName("");
        setPath("");
        setOpen(false);
      }}
    >
      <input
        value={name}
        onChange={(e) => setName(e.target.value)}
        placeholder="project name"
        className="bg-transparent outline-none border-b border-line text-[12px] py-1 placeholder:text-faint"
      />
      <input
        value={path}
        onChange={(e) => setPath(e.target.value)}
        placeholder="/path/to/workspace"
        className="bg-transparent outline-none border-b border-line text-[12px] py-1 font-mono placeholder:text-faint"
      />
      <div className="flex gap-1.5">
        <button type="submit" className="px-2 py-1 rounded-lg bg-accent text-white text-[11px]">
          Create
        </button>
        <button
          type="button"
          onClick={() => setOpen(false)}
          className="px-2 py-1 rounded-lg border border-line text-faint text-[11px]"
        >
          Cancel
        </button>
      </div>
    </form>
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
