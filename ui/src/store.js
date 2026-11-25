// Zustand store: reduces the WS event stream into conversation items
// (SPEC §20). The server is the only writer; the UI is a pure consumer
// plus command sender.
import { create } from "zustand";
import { api } from "./api";
import { itemsFromMessages, reduceEvent } from "./reducer";
import { WSConn } from "./ws";
let conn = null;
export const useStore = create((set, get) => ({
    sessions: [],
    current: null,
    items: [],
    running: false,
    connected: false,
    files: [],
    filePath: "",
    fileContent: "",
    error: null,
    async refreshSessions() {
        try {
            const sessions = await api.sessions();
            set({ sessions });
            if (!get().current && sessions.length)
                await get().select(sessions[0].id);
        }
        catch (e) {
            set({ error: String(e) });
        }
    },
    async select(id) {
        conn?.close();
        const rows = await api.messages(id).catch(() => []);
        set({
            current: id,
            items: itemsFromMessages(rows),
            running: false,
            filePath: "",
            fileContent: "",
        });
        conn = new WSConn(id, (ev) => {
            const s = get();
            const patch = { items: reduceEvent(s.items, ev) };
            if (ev.type === "session_status") {
                const st = String(ev.payload?.status ?? "");
                patch.running = st === "running" || st === "waiting_permission";
            }
            if (ev.type === "file_changed" && s.current === id)
                void get().refreshFiles();
            set(patch);
        }, (up) => set({ connected: up }));
        conn.connect();
        void get().refreshFiles();
    },
    async createSession() {
        const s = await api.createSession({ title: "New session" });
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
    async send(text, mode) {
        const id = get().current;
        if (!id)
            return;
        try {
            const res = await api.prompt(id, text, mode);
            if (res.state === "started")
                set({ running: true });
        }
        catch (e) {
            set({ error: String(e) });
        }
    },
    async stop() {
        const id = get().current;
        if (id)
            await api.stop(id).catch(() => { });
    },
    async answer(permId, granted) {
        const id = get().current;
        if (!id)
            return;
        conn?.send({ type: "permission", id: permId, granted });
        set({
            items: get().items.map((it) => it.kind === "permission" && it.permId === permId
                ? { ...it, answered: granted ? "yes" : "no" }
                : it),
        });
    },
    async withdraw(ijId) {
        const id = get().current;
        if (id && ijId)
            await api.withdraw(id, ijId).catch(() => { });
    },
    async promote(ijId) {
        const id = get().current;
        if (id && ijId)
            await api.promote(id, ijId).catch(() => { });
    },
    async refreshFiles(path = "") {
        const id = get().current;
        if (!id)
            return;
        try {
            set({ files: await api.files(id, path) });
        }
        catch {
            set({ files: [] });
        }
    },
    async openFile(path) {
        const id = get().current;
        if (!id)
            return;
        try {
            set({ filePath: path, fileContent: await api.file(id, path) });
        }
        catch (e) {
            set({ filePath: path, fileContent: `⚠ ${e}` });
        }
    },
    async saveFile(content) {
        const id = get().current;
        if (!id || !get().filePath)
            return;
        await api.putFile(id, get().filePath, content).catch((e) => set({ error: String(e) }));
    },
    close: () => conn?.close(),
}));
