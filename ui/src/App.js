import { jsx as _jsx, jsxs as _jsxs, Fragment as _Fragment } from "react/jsx-runtime";
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
    return (_jsxs("div", { className: "h-full flex", children: [_jsxs("aside", { className: "w-60 shrink-0 border-r border-line bg-panel/50 flex flex-col", children: [_jsxs("div", { className: "px-4 py-3.5 flex items-center gap-2", children: [_jsx("span", { className: "text-lg", children: "\u26A1" }), _jsx("span", { className: "font-semibold tracking-tight", children: "Genisys" }), _jsx("span", { className: `ml-auto w-2 h-2 rounded-full ${connected ? "bg-ok" : "bg-err"}`, title: connected ? "connected" : "reconnecting…" })] }), _jsx("button", { onClick: () => void create(), className: "mx-3 mb-2 px-3 py-2 rounded-xl bg-accent/90 hover:bg-accent text-white text-[13px] font-medium", children: "+ New session" }), _jsxs("div", { className: "flex-1 overflow-y-auto px-2 pb-3 flex flex-col gap-0.5", children: [sessions.map((s) => (_jsxs("div", { onClick: () => void select(s.id), className: `group flex items-center gap-2 px-2.5 py-2 rounded-lg cursor-pointer text-[13px] ${s.id === current ? "bg-panel2 text-ink" : "text-mute hover:bg-panel2/60"}`, children: [_jsx("span", { className: `w-1.5 h-1.5 rounded-full shrink-0 ${s.id === current && running
                                            ? "bg-accent2 animate-pulse"
                                            : s.pinned
                                                ? "bg-warn"
                                                : "bg-line"}` }), _jsx("span", { className: "truncate flex-1", children: s.title }), _jsx("button", { onClick: (e) => {
                                            e.stopPropagation();
                                            void archive(s.id);
                                        }, className: "opacity-0 group-hover:opacity-100 text-faint hover:text-err text-xs", title: "Archive session", children: "\u2715" })] }, s.id))), !sessions.length && (_jsx("div", { className: "text-xs text-faint px-2.5 py-2", children: "No sessions yet." }))] })] }), _jsx("main", { className: "flex-1 min-w-0 flex flex-col relative", children: current ? (_jsxs(_Fragment, { children: [_jsxs("header", { className: "px-6 py-3 border-b border-line flex items-center gap-3 text-xs text-mute", children: [_jsx("span", { className: "text-ink text-sm font-medium truncate", children: sess?.title ?? "…" }), _jsx("span", { className: "font-mono", children: sess?.model_ref }), running && _jsx("span", { className: "text-accent2 animate-pulse", children: "working" })] }), _jsx(MessageStream, {}), _jsx(Composer, {})] })) : (_jsx("div", { className: "flex-1 grid place-items-center text-mute", children: _jsxs("div", { className: "text-center", children: [_jsx("div", { className: "text-3xl mb-2", children: "\u26A1" }), _jsx("div", { className: "text-sm", children: "Create a session and ask Genisys to build, fix, or explain something." })] }) })) }), current && _jsx(RightPanel, {}), error && (_jsxs("div", { className: "fixed bottom-4 right-4 max-w-md border border-err/40 bg-panel rounded-xl px-4 py-3 text-sm text-err shadow-xl", onClick: () => useStore.setState({ error: null }), children: [error, " \u2715"] }))] }));
}
function RightPanel() {
    const files = useStore((s) => s.files);
    const refreshFiles = useStore((s) => s.refreshFiles);
    const openFile = useStore((s) => s.openFile);
    const filePath = useStore((s) => s.filePath);
    const fileContent = useStore((s) => s.fileContent);
    const saveFile = useStore((s) => s.saveFile);
    const [draft, setDraft] = useState("");
    const [tab, setTab] = useState("files");
    useEffect(() => setDraft(fileContent), [fileContent]);
    return (_jsxs("aside", { className: "w-96 shrink-0 border-l border-line bg-panel/50 flex flex-col", children: [_jsxs("div", { className: "flex border-b border-line text-[13px]", children: [["files", "editor"].map((t) => (_jsxs("button", { onClick: () => setTab(t), className: `px-4 py-2.5 capitalize ${tab === t ? "text-ink border-b-2 border-accent" : "text-faint hover:text-mute"}`, children: [t, t === "editor" && filePath && (_jsx("span", { className: "ml-1.5 text-faint font-mono text-xs", children: filePath.split("/").pop() }))] }, t))), _jsx("button", { onClick: () => void refreshFiles(), className: "ml-auto px-3 text-faint hover:text-ink", title: "Refresh", children: "\u27F3" })] }), tab === "files" ? (_jsxs("div", { className: "flex-1 overflow-y-auto p-2 font-mono text-[13px]", children: [files.map((f) => (_jsxs("div", { onClick: () => f.is_dir ? void refreshFiles(f.name) : void (openFile(f.name), setTab("editor")), className: "flex items-center gap-2 px-2 py-1.5 rounded-lg hover:bg-panel2 cursor-pointer", children: [_jsx("span", { className: f.is_dir ? "text-accent2" : "text-faint", children: f.is_dir ? "▸" : "·" }), _jsx("span", { className: "truncate flex-1", children: f.name }), !f.is_dir && _jsxs("span", { className: "text-faint text-xs", children: [f.size, "B"] })] }, f.name))), !files.length && _jsx("div", { className: "text-faint text-xs p-2", children: "Empty workspace." })] })) : (_jsxs("div", { className: "flex-1 flex flex-col", children: [_jsx("textarea", { value: draft, onChange: (e) => setDraft(e.target.value), className: "flex-1 bg-transparent outline-none resize-none p-3 font-mono text-[12.5px] leading-relaxed", spellCheck: false }), _jsxs("div", { className: "p-2 border-t border-line flex gap-2", children: [_jsx("button", { onClick: () => void saveFile(draft), disabled: draft === fileContent, className: "px-3 py-1.5 rounded-lg bg-accent text-white text-[13px] disabled:opacity-40", children: "Save" }), draft !== fileContent && _jsx("span", { className: "text-warn text-xs self-center", children: "modified" })] })] }))] }));
}
