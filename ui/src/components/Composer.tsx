// Composer (SPEC §5.3): model chip + picker, context meter, @-file
// palette with keyboard nav, attachments stub, and the interjection
// split-send (Queue + ⚡Interrupt while running, Shift inverts).

import { useEffect, useMemo, useRef, useState } from "react";
import { useStore } from "../store";

export function Composer() {
  const [text, setText] = useState("");
  const running = useStore((s) => s.running);
  const current = useStore((s) => s.current);
  const send = useStore((s) => s.send);
  const stop = useStore((s) => s.stop);
  const ta = useRef<HTMLTextAreaElement>(null);

  const submit = (mode?: "queue" | "interrupt") => {
    const t = text.trim();
    if (!t || !current) return;
    send(t, mode ?? (running ? "queue" : undefined));
    setText("");
    ta.current?.focus();
  };

  return (
    <div className="absolute bottom-0 inset-x-0 px-6 pb-4">
      <div className="max-w-3xl mx-auto relative">
        <Palette text={text} onPick={(name) => setText(pickName(text, name))} taRef={ta} />
        <div className="bg-panel border border-line rounded-2xl shadow-xl shadow-black/30 focus-within:border-accent/50">
          <textarea
            ref={ta}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (paletteOpen.value) return; // palette eats the keys
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                submit();
              }
            }}
            rows={Math.min(8, Math.max(1, text.split("\n").length))}
            placeholder={running ? "Type to interject…" : "Ask Genisys to do something… ( @ for files )"}
            className="w-full bg-transparent resize-none outline-none px-4 pt-3.5 pb-1 text-[14px] placeholder:text-faint"
          />
          <div className="flex items-center gap-2 px-3 pb-2.5">
            <ModelChip />
            <ContextMeter />
            <button
              className="text-faint hover:text-mute text-sm px-1"
              title="Attachments — lands with the document pipeline (P4)"
              disabled
            >
              +
            </button>
            {running && (
              <button
                onClick={() => void stop()}
                className="px-2.5 py-1 rounded-lg border border-err/40 text-err text-xs hover:bg-err/10"
                title="Stop the current task"
              >
                ■ Stop
              </button>
            )}
            <span className="text-[11px] text-faint ml-auto">
              Enter to send · Shift+Enter newline
            </span>
            <div className="flex rounded-lg overflow-hidden border border-line">
              <button
                onClick={(e) =>
                  submit(running ? (e.shiftKey ? "interrupt" : "queue") : undefined)
                }
                className="px-3.5 py-1.5 text-[13px] bg-accent text-white hover:brightness-110"
              >
                {running ? "Queue" : "Send"}
              </button>
              {running && (
                <button
                  onClick={() => submit("interrupt")}
                  className="px-2.5 py-1.5 text-[13px] bg-panel2 text-warn hover:bg-warn/10 border-l border-line"
                  title="Interrupt now (kills the running action)"
                >
                  ⚡
                </button>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

// --- @-palette ---------------------------------------------------------------

const paletteOpen = { value: false }; // shared flag for the textarea handler

function pickName(text: string, name: string): string {
  const idx = text.lastIndexOf("@");
  if (idx < 0) return text + name;
  return text.slice(0, idx) + name + " ";
}

function Palette({
  text,
  onPick,
  taRef,
}: {
  text: string;
  onPick: (name: string) => void;
  taRef: React.RefObject<HTMLTextAreaElement | null>;
}) {
  const files = useStore((s) => s.files);
  const [sel, setSel] = useState(0);

  const query = useMemo(() => {
    const m = /(^|\s)@([^\s]*)$/.exec(text);
    return m ? m[2] : null;
  }, [text]);

  const matches = useMemo(() => {
    if (query === null) return [];
    const q = query.toLowerCase();
    return files
      .filter((f) => f.name.toLowerCase().includes(q))
      .slice(0, 8)
      .map((f) => f.name);
  }, [query, files]);

  useEffect(() => setSel(0), [matches.length]);
  paletteOpen.value = matches.length > 0;
  if (!paletteOpen.value) return null;

  // the textarea owns focus, so attach the palette key handler there
  useEffect(() => {
    const ta = taRef.current;
    if (!ta) return;
    const h = (e: KeyboardEvent) => {
      if (!matches.length) return;
      if (e.key === "ArrowDown") { e.preventDefault(); setSel((s) => (s + 1) % matches.length); }
      else if (e.key === "ArrowUp") { e.preventDefault(); setSel((s) => (s - 1 + matches.length) % matches.length); }
      else if (e.key === "Tab" || e.key === "Enter") {
        e.preventDefault();
        onPick(matches[sel]);
      }
    };
    ta.addEventListener("keydown", h);
    return () => ta.removeEventListener("keydown", h);
  }, [matches, sel, onPick, taRef]);

  return (
    <div className="absolute bottom-full mb-2 left-4 w-72 bg-panel border border-line rounded-xl shadow-2xl py-1.5 z-20 text-[13px]">
      {matches.map((name, i) => (
        <div
          key={name}
          onClick={() => onPick(name)}
          className={`px-3 py-1.5 cursor-pointer flex gap-2 ${
            i === sel ? "bg-panel2 text-ink" : "text-mute"
          }`}
        >
          <span className="text-accent2">@</span>
          <span className="font-mono truncate">{name}</span>
        </div>
      ))}
      <div className="px-3 pt-1 text-[10px] text-faint">↑↓ navigate · Tab/Enter insert</div>
    </div>
  );
}

// --- model chip + context meter ------------------------------------------------

function ModelChip() {
  const [open, setOpen] = useState(false);
  const models = useStore((s) => s.models);
  const current = useStore((s) => s.current);
  const sessions = useStore((s) => s.sessions);
  const setModel = useStore((s) => s.setModel);
  const ref = sessions.find((s) => s.id === current)?.model_ref ?? "";

  return (
    <div className="relative">
      <button
        onClick={() => setOpen(!open)}
        className="flex items-center gap-1.5 px-2 py-1 rounded-lg border border-line text-[11px] text-mute hover:text-ink max-w-52"
        title="Model — click to switch (next turn)"
      >
        <span className="font-mono truncate">{ref || "model"}</span>
        <span className="text-faint">▾</span>
      </button>
      {open && (
        <div className="absolute bottom-full mb-2 left-0 w-72 bg-panel border border-line rounded-xl shadow-2xl py-1.5 z-20 text-[13px] max-h-64 overflow-y-auto">
          {models.map((m) => (
            <div
              key={m.ref}
              onClick={() => {
                void setModel(m.ref);
                setOpen(false);
              }}
              className={`px-3 py-1.5 cursor-pointer hover:bg-panel2 flex items-center ${
                m.ref === ref ? "text-accent" : "text-mute"
              }`}
            >
              <span className="font-mono truncate flex-1">{m.ref}</span>
              {m.context_window > 0 && (
                <span className="text-[10px] text-faint">
                  {Math.round(m.context_window / 1024)}k
                </span>
              )}
            </div>
          ))}
          {!models.length && (
            <div className="px-3 py-1.5 text-faint text-xs">No models in config.</div>
          )}
        </div>
      )}
    </div>
  );
}

function ContextMeter() {
  const tokens = useStore((s) => s.contextTokens);
  const window = useStore((s) => s.contextWindow);
  if (!tokens) return null;
  const pct = Math.min(100, Math.round((tokens / window) * 100));
  const color = pct > 85 ? "bg-err" : pct > 65 ? "bg-warn" : "bg-accent";
  return (
    <div
      className="flex items-center gap-1.5 text-[11px] text-faint"
      title={`${tokens.toLocaleString()} / ${window.toLocaleString()} context tokens`}
    >
      <div className="w-12 h-1.5 rounded-full bg-line overflow-hidden">
        <div className={`h-full ${color}`} style={{ width: `${pct}%` }} />
      </div>
      <span>
        {tokens >= 1000 ? `${(tokens / 1000).toFixed(1)}k` : tokens} · {pct}%
      </span>
    </div>
  );
}
