// Conversation spine renderer (SPEC §5.3): user bubbles, step traces
// ("Worked for …" with tool chips + diff cards), assistant bubbles,
// permission cards, interjection chips.

import { useState } from "react";
import type { Item } from "../types";
import { Markdown } from "./Markdown";
import { useStore } from "../store";

export function MessageStream() {
  const items = useStore((s) => s.items);
  const running = useStore((s) => s.running);
  return (
    <div className="flex-1 overflow-y-auto px-6 py-4">
      <div className="max-w-3xl mx-auto flex flex-col gap-3 pb-24">
        {items.map((it) => (
          <ItemView key={it.id} item={it} />
        ))}
        {running && items[items.length - 1]?.kind !== "step" && (
          <div className="text-mute text-sm animate-pulse">thinking…</div>
        )}
      </div>
    </div>
  );
}

function ItemView({ item }: { item: Item }) {
  switch (item.kind) {
    case "user":
      return (
        <div className="flex justify-end">
          <div className="max-w-[80%] bg-accent/20 border border-accent/30 rounded-2xl rounded-br-md px-4 py-2.5 text-[14px] whitespace-pre-wrap">
            {item.text}
          </div>
        </div>
      );
    case "assistant":
      return (
        <div className="pl-1">
          <Markdown text={item.text} />
        </div>
      );
    case "step":
      return <StepTrace item={item} />;
    case "permission":
      return <PermissionCard item={item} />;
    case "interjection":
      return (
        <div className="flex justify-center">
          <div
            className={`text-xs px-3 py-1.5 rounded-full border ${
              item.state === "delivered"
                ? "border-line text-faint"
                : "border-warn/40 text-warn bg-warn/5"
            }`}
          >
            ⇩ {item.mode === "interrupt" ? "interrupt" : "queued"}:{" "}
            <span className="line-clamp-1 inline-block max-w-64 align-bottom">
              {item.text}
            </span>
            {item.state === "delivered" && " · delivered"}
          </div>
        </div>
      );
    case "error":
      return (
        <div className="border border-err/40 bg-err/5 rounded-xl px-4 py-2.5 text-sm text-err">
          ⚠ {item.text}
        </div>
      );
  }
}

function StepTrace({ item }: { item: Extract<Item, { kind: "step" }> }) {
  const [open, setOpen] = useState(false);
  const running = !item.done && item.tools.some((t) => t.running);
  const secs = item.startedAt ? Math.max(1, Math.round((Date.now() - item.startedAt) / 1000)) : 0;
  return (
    <div className="border border-line rounded-xl bg-panel/60 overflow-hidden">
      <button
        onClick={() => setOpen(!open)}
        className="w-full flex items-center gap-2 px-3.5 py-2.5 text-left text-[13px] text-mute hover:bg-panel2/60"
      >
        <span className={running ? "text-accent2 animate-pulse" : ""}>
          {running ? "◌" : "✓"}
        </span>
        {running ? "Working…" : `Worked${secs ? ` for ${secs}s` : ""}`}
        <span className="ml-auto text-faint text-xs">{open ? "▾" : "▸"}</span>
      </button>
      {(open || running) && (
        <div className="px-3.5 pb-2.5 flex flex-col gap-1.5">
          {item.tools.map((t) => (
            <div key={t.callId} className="flex items-start gap-2 text-xs font-mono">
              <span className={t.running ? "text-accent2" : t.ok ? "text-ok" : "text-err"}>
                {t.running ? "◌" : t.ok ? "✓" : "✗"}
              </span>
              <span className="text-ink/90 shrink-0">{t.tool}</span>
              <span className="text-faint truncate max-w-72" title={t.args}>
                {t.args}
              </span>
              {t.summary && (
                <span className="text-mute truncate flex-1" title={t.summary}>
                  {t.summary.split("\n")[0]}
                </span>
              )}
            </div>
          ))}
          {item.files.map((f, i) => (
            <DiffCard key={i} path={f.path} change={f.change} diff={f.diff} />
          ))}
        </div>
      )}
    </div>
  );
}

function DiffCard({ path, change, diff }: { path: string; change: string; diff?: string }) {
  const [open, setOpen] = useState(false);
  const mark = change === "created" ? "+" : change === "deleted" ? "−" : "~";
  const color = change === "deleted" ? "text-err" : change === "created" ? "text-ok" : "text-warn";
  const adds = diff ? diff.split("\n").filter((l) => l.startsWith("+") && !l.startsWith("+++")).length : 0;
  const dels = diff ? diff.split("\n").filter((l) => l.startsWith("-") && !l.startsWith("---")).length : 0;
  return (
    <div className="text-xs">
      <button
        onClick={() => diff && setOpen(!open)}
        className="flex items-center gap-2 hover:underline decoration-line"
      >
        <span className={`${color} font-mono`}>{mark}</span>
        <span className="font-mono text-ink/90">{path}</span>
        {diff && (
          <span className="font-mono">
            <span className="text-ok">+{adds}</span>{" "}
            <span className="text-err">−{dels}</span>
          </span>
        )}
      </button>
      {open && diff && (
        <pre className="mt-1.5 bg-bg border border-line rounded-lg p-2.5 overflow-x-auto font-mono text-[11.5px] leading-snug max-h-72 overflow-y-auto">
          {diff.split("\n").map((l, i) => (
            <div
              key={i}
              className={
                l.startsWith("+") && !l.startsWith("+++")
                  ? "text-ok/90"
                  : l.startsWith("-") && !l.startsWith("---")
                    ? "text-err/90"
                    : l.startsWith("@@")
                      ? "text-accent2"
                      : "text-mute"
              }
            >
              {l || " "}
            </div>
          ))}
        </pre>
      )}
    </div>
  );
}

function PermissionCard({ item }: { item: Extract<Item, { kind: "permission" }> }) {
  const answer = useStore((s) => s.answer);
  const done = item.answered !== undefined;
  return (
    <div className="border border-warn/40 bg-warn/5 rounded-xl px-4 py-3">
      <div className="text-sm">
        <span className="text-warn">⚿ Permission needed</span>
        <span className="text-mute"> — </span>
        <span className="font-mono text-[13px]">{item.mode}</span> access to{" "}
        <span className="font-mono text-[13px]">{item.path}</span>
      </div>
      {item.reason && <div className="text-xs text-mute mt-1">{item.reason}</div>}
      {!done ? (
        <div className="flex gap-2 mt-2.5">
          <button
            onClick={() => answer(item.permId, true)}
            className="px-3.5 py-1.5 rounded-lg bg-accent text-white text-[13px] hover:brightness-110"
          >
            Allow
          </button>
          <button
            onClick={() => answer(item.permId, false)}
            className="px-3.5 py-1.5 rounded-lg border border-line text-mute text-[13px] hover:bg-panel2"
          >
            Deny
          </button>
        </div>
      ) : (
        <div className="text-xs text-faint mt-2">
          {item.answered === "yes" ? "allowed" : "denied"}
        </div>
      )}
    </div>
  );
}
