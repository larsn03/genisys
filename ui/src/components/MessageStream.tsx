// Conversation spine renderer (SPEC §5.3): user bubbles, step traces
// ("Worked for …" with tool chips + diff cards + per-step revert),
// assistant bubbles with action row, permission cards, interjection
// chips with promote/withdraw, interrupt confirmation card.

import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import type { Item } from "../types";
import { Markdown } from "./Markdown";
import { useStore } from "../store";

const WINDOW = 150; // render tail only; "show older" expands (P3-10)

export function MessageStream() {
  const items = useStore((s) => s.items);
  const showAll = useStore((s) => s.showAll);
  const setShowAll = useStore((s) => s.setShowAll);
  const running = useStore((s) => s.running);
  const boxRef = useRef<HTMLDivElement>(null);
  const stick = useRef(true);

  const visible = showAll || items.length <= WINDOW ? items : items.slice(-WINDOW);

  // autoscroll while the user is at the bottom
  useEffect(() => {
    const el = boxRef.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [items]);

  return (
    <div
      ref={boxRef}
      className="flex-1 overflow-y-auto px-6 py-4"
      onScroll={(e) => {
        const el = e.currentTarget;
        stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
      }}
    >
      <div className="max-w-3xl mx-auto flex flex-col gap-3 pb-24">
        {!items.length && !running && (
          <div className="pt-20 flex flex-col items-center text-center select-none">
            <div className="w-12 h-12 rounded-2xl bg-accent/15 border border-accent/25 grid place-items-center text-[22px]">
              ⚡
            </div>
            <div className="mt-4 text-[15px] font-medium text-ink">
              What should we work on?
            </div>
            <div className="mt-1.5 text-[13px] text-mute max-w-sm leading-relaxed">
              Ask Genisys to build, fix or explain something. Type{" "}
              <code className="font-mono text-[12px] bg-panel2 rounded px-1.5 py-0.5">@</code>{" "}
              to reference files,{" "}
              <code className="font-mono text-[12px] bg-panel2 rounded px-1.5 py-0.5">@skill:</code>{" "}
              to apply a skill.
            </div>
          </div>
        )}
        {items.length > WINDOW && !showAll && (
          <button
            onClick={() => setShowAll(true)}
            className="self-center text-xs text-faint hover:text-mute px-3 py-1.5 rounded-full border border-line"
          >
            ↑ {items.length - WINDOW} earlier items — show all
          </button>
        )}
        {visible.map((it) => (
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
      return <AssistantBubble item={item} />;
    case "step":
      // pure-text steps have no chips to show — skip the trace chrome
      if (!item.tools.length && !item.files.length) return null;
      return <StepTrace item={item} />;
    case "permission":
      return <PermissionCard item={item} />;
    case "interjection":
      return <InterjectionChip item={item} />;
    case "interrupt":
      return (
        <div className="flex justify-center">
          <div className="text-xs px-3 py-1.5 rounded-full border border-accent2/50 text-accent2 bg-accent2/5">
            ⚡ Interrupting — stopping the running action
          </div>
        </div>
      );
    case "deliver":
      return <DeliverCard item={item} />;
    case "error":
      return <ErrorCard item={item} />;
    case "marker":
      return (
        <div className="flex items-center gap-3 py-1 text-[11px] text-faint">
          <div className="h-px flex-1 bg-line" />
          <span className="shrink-0">⇲ {item.text}</span>
          <div className="h-px flex-1 bg-line" />
        </div>
      );
  }
}

// SPEC §23: endpoint failures surface as cards with Retry · Switch model ·
// Edit last message (+ Compact now, since overflow is the common cause).
function ErrorCard({ item }: { item: Extract<Item, { kind: "error" }> }) {
  const retryLast = useStore((s) => s.retryLast);
  const setComposerDraft = useStore((s) => s.setComposerDraft);
  const setPickModel = useStore((s) => s.setPickModel);
  const compactNow = useStore((s) => s.compactNow);
  const lastUserText = useStore((s) => s.lastUserText);
  const overflow = /context|length|token/i.test(item.text);

  const btn =
    "text-[12px] px-2.5 py-1 rounded-lg border border-err/40 text-err hover:bg-err/10";
  return (
    <div className="border border-err/40 bg-err/5 rounded-xl px-4 py-3 text-sm">
      <div className="text-err">⚠ {item.text}</div>
      <div className="mt-2.5 flex flex-wrap gap-2">
        <button className={btn} onClick={() => void retryLast()}>
          ↻ Retry
        </button>
        <button className={btn} onClick={() => setPickModel(true)}>
          ⇄ Switch model
        </button>
        {lastUserText && (
          <button
            className={btn}
            onClick={() => setComposerDraft(lastUserText)}
          >
            ✎ Edit last message
          </button>
        )}
        {overflow && (
          <button className={btn} onClick={() => void compactNow()}>
            ⇲ Compact now
          </button>
        )}
      </div>
    </div>
  );
}

function AssistantBubble({ item }: { item: Extract<Item, { kind: "assistant" }> }) {
  const [copied, setCopied] = useState(false);
  const items = useStore((s) => s.items);
  const send = useStore((s) => s.send);
  const running = useStore((s) => s.running);

  const lastUser = [...items].reverse().find((i) => i.kind === "user");
  const isLastAssistant =
    [...items].reverse().find((i) => i.kind === "assistant")?.id === item.id;

  return (
    <div className="group pl-1">
      <Markdown text={item.text} />
      <div className="flex gap-1 mt-1 opacity-0 group-hover:opacity-100 transition-opacity">
        <button
          onClick={() => {
            void navigator.clipboard?.writeText(item.text);
            setCopied(true);
            setTimeout(() => setCopied(false), 1200);
          }}
          className="text-[11px] text-faint hover:text-mute px-1.5 py-0.5 rounded hover:bg-panel2"
        >
          {copied ? "copied ✓" : "copy"}
        </button>
        {isLastAssistant && lastUser?.kind === "user" && !running && (
          <button
            onClick={() => void send(lastUser.text)}
            className="text-[11px] text-faint hover:text-mute px-1.5 py-0.5 rounded hover:bg-panel2"
          >
            ↻ retry
          </button>
        )}
      </div>
    </div>
  );
}

function StepTrace({ item }: { item: Extract<Item, { kind: "step" }> }) {
  const [open, setOpen] = useState(false);
  const [confirmRevert, setConfirmRevert] = useState(false);
  const revertStep = useStore((s) => s.revertStep);
  const running = !item.done && item.tools.some((t) => t.running);
  const secs = item.startedAt
    ? Math.max(1, Math.round((Date.now() - item.startedAt) / 1000))
    : 0;
  return (
    <div className="border border-line rounded-xl bg-panel/60 overflow-hidden group/step">
      <div className="flex items-center gap-2 pr-3">
        <button
          onClick={() => setOpen(!open)}
          className="flex-1 flex items-center gap-2 px-3.5 py-2.5 text-left text-[13px] text-mute hover:bg-panel2/60"
        >
          <span className={running ? "text-accent2 animate-pulse" : ""}>
            {running ? "◌" : "✓"}
          </span>
          {running ? "Working…" : `Worked${secs ? ` for ${secs}s` : ""}`}
          <span className="ml-auto text-faint text-xs">{open ? "▾" : "▸"}</span>
        </button>
        {item.files.length > 0 &&
          (confirmRevert ? (
            <span className="flex gap-1 text-[11px]">
              <button
                onClick={() => {
                  void revertStep(item.step);
                  setConfirmRevert(false);
                }}
                className="px-2 py-0.5 rounded bg-err/80 text-white"
              >
                Revert {item.files.length} file(s)?
              </button>
              <button
                onClick={() => setConfirmRevert(false)}
                className="px-1.5 py-0.5 rounded border border-line text-faint"
              >
                No
              </button>
            </span>
          ) : (
            <button
              onClick={() => setConfirmRevert(true)}
              className="opacity-0 group-hover/step:opacity-100 text-[11px] text-warn hover:text-warn/80 px-1.5 py-0.5 rounded hover:bg-warn/10"
              title={`Roll the ${item.files.length} file(s) changed in ${item.step} back`}
            >
              ↺ Revert step
            </button>
          ))}
      </div>
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
  const openFile = useStore((s) => s.openFile);
  const mark = change === "created" ? "+" : change === "deleted" ? "−" : "~";
  const color =
    change === "deleted" ? "text-err" : change === "created" ? "text-ok" : "text-warn";
  const adds = diff
    ? diff.split("\n").filter((l) => l.startsWith("+") && !l.startsWith("+++")).length
    : 0;
  const dels = diff
    ? diff.split("\n").filter((l) => l.startsWith("-") && !l.startsWith("---")).length
    : 0;
  return (
    <div className="text-xs">
      <div className="flex items-center gap-2">
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
        {change !== "deleted" && (
          <button
            onClick={() => void openFile(path)}
            className="text-[10px] text-faint hover:text-mute"
            title="Open in editor"
          >
            ✎ open
          </button>
        )}
      </div>
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

function DeliverCard({ item }: { item: Extract<Item, { kind: "deliver" }> }) {
  const current = useStore((s) => s.current);
  const openPreview = useStore((s) => s.openPreview);
  if (!current) return null;
  const name = item.path.split("/").pop();
  return (
    <div className="border border-accent/30 bg-accent/5 rounded-xl px-4 py-3 flex items-center gap-3">
      <span className="text-lg">{item.attached ? "📎" : "📦"}</span>
      <div className="flex-1 min-w-0">
        <div className="text-[13px] truncate">
          {item.attached ? "Attached" : "Delivered"}:{" "}
          <span className="font-mono">{name}</span>
        </div>
        {!item.attached && (
          <div className="text-[11px] text-faint font-mono truncate">{item.path}</div>
        )}
      </div>
      <button
        onClick={() => openPreview(item.path)}
        className="px-3 py-1.5 rounded-lg bg-accent text-white text-[12px] hover:brightness-110"
      >
        Preview
      </button>
      <a
        href={api.fileURL(current, item.path)}
        download={name}
        className="px-3 py-1.5 rounded-lg border border-line text-mute text-[12px] hover:bg-panel2"
      >
        ↓
      </a>
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

function InterjectionChip({ item }: { item: Extract<Item, { kind: "interjection" }> }) {
  const promote = useStore((s) => s.promote);
  const withdraw = useStore((s) => s.withdraw);
  const pending = item.state === "pending";
  return (
    <div className="flex justify-center">
      <div
        className={`flex items-center gap-2 text-xs px-3 py-1.5 rounded-full border ${
          pending
            ? item.mode === "interrupt"
              ? "border-accent2/50 text-accent2 bg-accent2/5"
              : "border-warn/40 text-warn bg-warn/5"
            : "border-line text-faint"
        }`}
      >
        <span>{item.mode === "interrupt" ? "⚡" : "⇩"}</span>
        <span className="line-clamp-1 inline-block max-w-64">{item.text}</span>
        {pending && item.ijId ? (
          <span className="flex gap-1 ml-1">
            {item.mode !== "interrupt" && (
              <button
                onClick={() => void promote(item.ijId)}
                className="hover:text-accent2"
                title="Promote to interrupt (act now)"
              >
                ⚡
              </button>
            )}
            <button
              onClick={() => void withdraw(item.ijId)}
              className="hover:text-err"
              title="Withdraw"
            >
              ✕
            </button>
          </span>
        ) : null}
        {!pending && <span>· delivered</span>}
      </div>
    </div>
  );
}
