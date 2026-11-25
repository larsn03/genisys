// Zustand store: reduces the WS event stream into conversation items
// (SPEC §20). The server is the only writer; the UI is a pure consumer
// plus command sender.

import { create } from "zustand";
import { api } from "./api";
import { itemsFromMessages, reduceEvent } from "./reducer";
import { WSConn } from "./ws";
import type { CheckpointRow, GitStatus, Item, ModelInfo, SessionRow } from "./types";

interface PaneState {
  left: number;
  right: number;
  leftOpen: boolean;
  rightOpen: boolean;
}

const LAYOUT_KEY = "genisys.layout";
const loadLayout = (): PaneState => {
  try {
    return { left: 240, right: 384, leftOpen: true, rightOpen: true, ...JSON.parse(localStorage.getItem(LAYOUT_KEY) ?? "{}") };
  } catch {
    return { left: 240, right: 384, leftOpen: true, rightOpen: true };
  }
};

interface State {
  sessions: SessionRow[];
  projects: { id: string; name: string; kind: string; workspace_path: string }[];
  current: string | null;
  items: Item[];
  showAll: boolean; // windowing escape hatch (long replays)
  running: boolean;
  connected: boolean;

  files: { name: string; is_dir: boolean; size: number }[];
  dirPath: string;
  git: GitStatus | null;
  filePath: string;
  fileContent: string;
  checkpoints: CheckpointRow[];

  models: ModelInfo[];
  defaultModel: string;
  contextTokens: number; // last prompt_tokens = live context size
  contextWindow: number;

  layout: PaneState;
  error: string | null;
  preview: string | null; // path currently open in the preview overlay
  browser: { url: string; nonce: number } | null; // in-app browser pane (SPEC §5.4)
  browserOrigins: string[]; // allow-list from config
  skills: import("./types").SkillInfo[]; // @-palette feed (SPEC §14)
  selectedAt: number; // ms epoch of last session select (focus-or-open heuristic)

  refreshSessions: () => Promise<void>;
  refreshProjects: () => Promise<void>;
  createProject: (name: string, path: string) => Promise<void>;
  select: (id: string) => Promise<void>;
  createSession: (projectId?: string) => Promise<void>;
  archive: (id: string) => Promise<void>;
  setModel: (ref: string) => Promise<void>;
  loadModels: () => Promise<void>;

  send: (text: string, mode?: "queue" | "interrupt") => Promise<void>;
  stop: () => Promise<void>;
  answer: (permId: number, granted: boolean) => Promise<void>;
  withdraw: (ijId: string) => Promise<void>;
  promote: (ijId: string) => Promise<void>;

  refreshFiles: (path?: string) => Promise<void>;
  openFile: (path: string) => Promise<void>;
  saveFile: (content: string) => Promise<void>;
  refreshGit: () => Promise<void>;
  refreshCheckpoints: () => Promise<void>;
  revertStep: (stepId: string) => Promise<void>;

  openBrowser: (url: string) => void;
  closeBrowser: () => void;
  openPreview: (path: string) => void;
  closePreview: () => void;
  setShowAll: (v: boolean) => void;
  setLayout: (patch: Partial<PaneState>) => void;
  close: () => void;
}

let conn: WSConn | null = null;

export const useStore = create<State>((set, get) => ({
  sessions: [],
  projects: [],
  current: null,
  items: [],
  showAll: false,
  running: false,
  connected: false,

  files: [],
  dirPath: "",
  git: null,
  filePath: "",
  fileContent: "",
  checkpoints: [],

  models: [],
  defaultModel: "",
  contextTokens: 0,
  contextWindow: 32768,

  layout: loadLayout(),
  error: null,
  preview: null,
  browser: null,
  browserOrigins: [],
  skills: [],
  selectedAt: 0,

  async loadModels() {
    try {
      const { default: d, models } = await api.models();
      set({ models, defaultModel: d });
    } catch {
      /* endpoint-less config */
    }
  },

  async refreshSessions() {
    try {
      const sessions = await api.sessions();
      set({ sessions });
      if (!get().current && sessions.length) await get().select(sessions[0].id);
    } catch (e) {
      set({ error: String(e) });
    }
  },

  async refreshProjects() {
    try {
      set({ projects: await api.projects() });
    } catch {
      set({ projects: [] });
    }
  },

  async createProject(name, path) {
    try {
      await api.createProject({ name, kind: "code", workspace_path: path, allow_coding: true });
      await get().refreshProjects();
    } catch (e) {
      set({ error: String(e) });
    }
  },

  async select(id) {
    conn?.close();
    const rows = await api.messages(id).catch(() => []);
    const sess = get().sessions.find((s) => s.id === id);
    set({
      current: id,
      items: itemsFromMessages(rows),
      showAll: false,
      running: false,
      filePath: "",
      fileContent: "",
      dirPath: "",
      contextTokens: 0,
      browser: null,
      selectedAt: Date.now(),
      contextWindow:
        get().models.find((m) => m.ref === sess?.model_ref)?.context_window || 32768,
    });
    conn = new WSConn(
      id,
      (ev) => {
        const s = get();
        const patch: Partial<State> = { items: reduceEvent(s.items, ev) };
        if (ev.type === "session_status") {
          const st = String(ev.payload?.status ?? "");
          patch.running = st === "running" || st === "waiting_permission";
        }
        if (ev.type === "usage") {
          patch.contextTokens = Number(ev.payload?.prompt_tokens ?? 0) || s.contextTokens;
        }
        if (ev.type === "ui_open_url") {
          const url = String(ev.payload?.url ?? "");
          if (url) patch.browser = { url, nonce: (get().browser?.nonce ?? 0) + 1 };
        }
        if (ev.type === "file_changed" && s.current === id) {
          void get().refreshFiles(s.dirPath);
          void get().refreshGit();
        }
        set(patch);
      },
      (up) => set({ connected: up }),
    );
    conn.connect();
    void get().refreshFiles("");
    void get().refreshGit();
    void get().refreshCheckpoints();
    if (get().skills.length === 0) {
      api
        .skills()
        .then((r) => set({ skills: r.skills }))
        .catch(() => {});
    }
    if (get().browserOrigins.length === 0) {
      api
        .browserOrigins()
        .then((o) => set({ browserOrigins: o.origins }))
        .catch(() => {});
    }
  },

  async createSession(projectId) {
    const s = await api.createSession({ title: "New session", project_id: projectId ?? "" });
    set({ error: null });
    await get().refreshSessions();
    await get().select(s.id);
  },

  async archive(id) {
    await api.archiveSession(id);
    if (get().current === id) {
      conn?.close();
      set({ current: null, items: [] });
    }
    await get().refreshSessions();
  },

  async setModel(ref) {
    const id = get().current;
    if (!id) return;
    await api.patchSession(id, { model_ref: ref }).catch((e) => set({ error: String(e) }));
    set({
      contextWindow: get().models.find((m) => m.ref === ref)?.context_window || 32768,
    });
    await get().refreshSessions();
  },

  async send(text, mode) {
    const id = get().current;
    if (!id) return;
    // @skill:name chips → skills[] on the prompt API (SPEC §14 explicit
    // invocation); the harness appends the read-SKILL.md-first line.
    const skillNames = [...text.matchAll(/(?:^|\s)@skill:([\w.-]+)/g)].map((m) => m[1]);
    const clean = text.replace(/(?:^|\s)@skill:([\w.-]+)/g, " ").trim();
    try {
      const res = await api.prompt(id, clean, mode, skillNames.length ? skillNames : undefined);
      if (res.state === "started") set({ running: true });
    } catch (e) {
      set({ error: String(e) });
    }
  },

  async stop() {
    const id = get().current;
    if (id) await api.stop(id).catch(() => {});
  },

  async answer(permId, granted) {
    const id = get().current;
    if (!id) return;
    conn?.send({ type: "permission", id: permId, granted });
    set({
      items: get().items.map((it) =>
        it.kind === "permission" && it.permId === permId
          ? { ...it, answered: granted ? ("yes" as const) : ("no" as const) }
          : it,
      ),
    });
  },

  async withdraw(ijId) {
    const id = get().current;
    if (id && ijId) {
      await api.withdraw(id, ijId).catch(() => {});
      set({
        items: get().items.filter(
          (it) => !(it.kind === "interjection" && it.ijId === ijId),
        ),
      });
    }
  },

  async promote(ijId) {
    const id = get().current;
    if (id && ijId) {
      await api.promote(id, ijId).catch(() => {});
      set({
        items: get().items.map((it) =>
          it.kind === "interjection" && it.ijId === ijId ? { ...it, mode: "interrupt" } : it,
        ),
      });
    }
  },

  async refreshFiles(path = "") {
    const id = get().current;
    if (!id) return;
    try {
      set({ files: await api.files(id, path), dirPath: path });
    } catch {
      set({ files: [] });
    }
  },

  async refreshGit() {
    const id = get().current;
    if (!id) return;
    try {
      set({ git: await api.gitStatus(id) });
    } catch {
      set({ git: null });
    }
  },

  async refreshCheckpoints() {
    const id = get().current;
    if (!id) return;
    try {
      set({ checkpoints: await api.checkpoints(id) });
    } catch {
      set({ checkpoints: [] });
    }
  },

  async revertStep(stepId) {
    const id = get().current;
    if (!id) return;
    try {
      await api.revert(id, stepId);
      set({ error: null });
      void get().refreshFiles(get().dirPath);
      void get().refreshGit();
    } catch (e) {
      set({ error: String(e) });
    }
  },

  async openFile(path) {
    const id = get().current;
    if (!id) return;
    try {
      set({ filePath: path, fileContent: await api.file(id, path) });
    } catch (e) {
      set({ filePath: path, fileContent: `⚠ ${e}` });
    }
  },

  async saveFile(content) {
    const id = get().current;
    if (!id || !get().filePath) return;
    await api
      .putFile(id, get().filePath, content)
      .catch((e) => set({ error: String(e) }));
    void get().refreshGit();
    void get().refreshCheckpoints();
  },

  openBrowser: (url: string) =>
    set((s) => ({ browser: { url, nonce: (s.browser?.nonce ?? 0) + 1 } })),
  closeBrowser: () => set({ browser: null }),
  openPreview: (path) => set({ preview: path }),
  closePreview: () => set({ preview: null }),

  setShowAll: (v) => set({ showAll: v }),

  setLayout: (patch) => {
    const layout = { ...get().layout, ...patch };
    localStorage.setItem(LAYOUT_KEY, JSON.stringify(layout));
    set({ layout });
  },

  close: () => conn?.close(),
}));
