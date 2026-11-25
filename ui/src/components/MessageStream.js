import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
// Conversation spine renderer (SPEC §5.3): user bubbles, step traces
// ("Worked for …" with tool chips + diff cards), assistant bubbles,
// permission cards, interjection chips.
import { useState } from "react";
import { Markdown } from "./Markdown";
import { useStore } from "../store";
export function MessageStream() {
    const items = useStore((s) => s.items);
    const running = useStore((s) => s.running);
    return (_jsx("div", { className: "flex-1 overflow-y-auto px-6 py-4", children: _jsxs("div", { className: "max-w-3xl mx-auto flex flex-col gap-3 pb-24", children: [items.map((it) => (_jsx(ItemView, { item: it }, it.id))), running && items[items.length - 1]?.kind !== "step" && (_jsx("div", { className: "text-mute text-sm animate-pulse", children: "thinking\u2026" }))] }) }));
}
function ItemView({ item }) {
    switch (item.kind) {
        case "user":
            return (_jsx("div", { className: "flex justify-end", children: _jsx("div", { className: "max-w-[80%] bg-accent/20 border border-accent/30 rounded-2xl rounded-br-md px-4 py-2.5 text-[14px] whitespace-pre-wrap", children: item.text }) }));
        case "assistant":
            return (_jsx("div", { className: "pl-1", children: _jsx(Markdown, { text: item.text }) }));
        case "step":
            return _jsx(StepTrace, { item: item });
        case "permission":
            return _jsx(PermissionCard, { item: item });
        case "interjection":
            return (_jsx("div", { className: "flex justify-center", children: _jsxs("div", { className: `text-xs px-3 py-1.5 rounded-full border ${item.state === "delivered"
                        ? "border-line text-faint"
                        : "border-warn/40 text-warn bg-warn/5"}`, children: ["\u21E9 ", item.mode === "interrupt" ? "interrupt" : "queued", ":", " ", _jsx("span", { className: "line-clamp-1 inline-block max-w-64 align-bottom", children: item.text }), item.state === "delivered" && " · delivered"] }) }));
        case "error":
            return (_jsxs("div", { className: "border border-err/40 bg-err/5 rounded-xl px-4 py-2.5 text-sm text-err", children: ["\u26A0 ", item.text] }));
    }
}
function StepTrace({ item }) {
    const [open, setOpen] = useState(false);
    const running = !item.done && item.tools.some((t) => t.running);
    const secs = item.startedAt ? Math.max(1, Math.round((Date.now() - item.startedAt) / 1000)) : 0;
    return (_jsxs("div", { className: "border border-line rounded-xl bg-panel/60 overflow-hidden", children: [_jsxs("button", { onClick: () => setOpen(!open), className: "w-full flex items-center gap-2 px-3.5 py-2.5 text-left text-[13px] text-mute hover:bg-panel2/60", children: [_jsx("span", { className: running ? "text-accent2 animate-pulse" : "", children: running ? "◌" : "✓" }), running ? "Working…" : `Worked${secs ? ` for ${secs}s` : ""}`, _jsx("span", { className: "ml-auto text-faint text-xs", children: open ? "▾" : "▸" })] }), (open || running) && (_jsxs("div", { className: "px-3.5 pb-2.5 flex flex-col gap-1.5", children: [item.tools.map((t) => (_jsxs("div", { className: "flex items-start gap-2 text-xs font-mono", children: [_jsx("span", { className: t.running ? "text-accent2" : t.ok ? "text-ok" : "text-err", children: t.running ? "◌" : t.ok ? "✓" : "✗" }), _jsx("span", { className: "text-ink/90 shrink-0", children: t.tool }), _jsx("span", { className: "text-faint truncate max-w-72", title: t.args, children: t.args }), t.summary && (_jsx("span", { className: "text-mute truncate flex-1", title: t.summary, children: t.summary.split("\n")[0] }))] }, t.callId))), item.files.map((f, i) => (_jsx(DiffCard, { path: f.path, change: f.change, diff: f.diff }, i)))] }))] }));
}
function DiffCard({ path, change, diff }) {
    const [open, setOpen] = useState(false);
    const mark = change === "created" ? "+" : change === "deleted" ? "−" : "~";
    const color = change === "deleted" ? "text-err" : change === "created" ? "text-ok" : "text-warn";
    const adds = diff ? diff.split("\n").filter((l) => l.startsWith("+") && !l.startsWith("+++")).length : 0;
    const dels = diff ? diff.split("\n").filter((l) => l.startsWith("-") && !l.startsWith("---")).length : 0;
    return (_jsxs("div", { className: "text-xs", children: [_jsxs("button", { onClick: () => diff && setOpen(!open), className: "flex items-center gap-2 hover:underline decoration-line", children: [_jsx("span", { className: `${color} font-mono`, children: mark }), _jsx("span", { className: "font-mono text-ink/90", children: path }), diff && (_jsxs("span", { className: "font-mono", children: [_jsxs("span", { className: "text-ok", children: ["+", adds] }), " ", _jsxs("span", { className: "text-err", children: ["\u2212", dels] })] }))] }), open && diff && (_jsx("pre", { className: "mt-1.5 bg-bg border border-line rounded-lg p-2.5 overflow-x-auto font-mono text-[11.5px] leading-snug max-h-72 overflow-y-auto", children: diff.split("\n").map((l, i) => (_jsx("div", { className: l.startsWith("+") && !l.startsWith("+++")
                        ? "text-ok/90"
                        : l.startsWith("-") && !l.startsWith("---")
                            ? "text-err/90"
                            : l.startsWith("@@")
                                ? "text-accent2"
                                : "text-mute", children: l || " " }, i))) }))] }));
}
function PermissionCard({ item }) {
    const answer = useStore((s) => s.answer);
    const done = item.answered !== undefined;
    return (_jsxs("div", { className: "border border-warn/40 bg-warn/5 rounded-xl px-4 py-3", children: [_jsxs("div", { className: "text-sm", children: [_jsx("span", { className: "text-warn", children: "\u26BF Permission needed" }), _jsx("span", { className: "text-mute", children: " \u2014 " }), _jsx("span", { className: "font-mono text-[13px]", children: item.mode }), " access to", " ", _jsx("span", { className: "font-mono text-[13px]", children: item.path })] }), item.reason && _jsx("div", { className: "text-xs text-mute mt-1", children: item.reason }), !done ? (_jsxs("div", { className: "flex gap-2 mt-2.5", children: [_jsx("button", { onClick: () => answer(item.permId, true), className: "px-3.5 py-1.5 rounded-lg bg-accent text-white text-[13px] hover:brightness-110", children: "Allow" }), _jsx("button", { onClick: () => answer(item.permId, false), className: "px-3.5 py-1.5 rounded-lg border border-line text-mute text-[13px] hover:bg-panel2", children: "Deny" })] })) : (_jsx("div", { className: "text-xs text-faint mt-2", children: item.answered === "yes" ? "allowed" : "denied" }))] }));
}
