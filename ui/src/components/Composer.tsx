// Composer with the interjection split-send (SPEC §5.3/§7.4): while the
// agent runs, Send queues and the bolt interrupts (Shift inverts).

import { useRef, useState } from "react";
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
      <div className="max-w-3xl mx-auto">
        <div className="bg-panel border border-line rounded-2xl shadow-xl shadow-black/30 focus-within:border-accent/50">
          <textarea
            ref={ta}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                submit();
              }
            }}
            rows={Math.min(8, Math.max(1, text.split("\n").length))}
            placeholder={
              running ? "Type to interject…" : "Ask Genisys to do something…"
            }
            className="w-full bg-transparent resize-none outline-none px-4 pt-3.5 pb-1 text-[14px] placeholder:text-faint"
          />
          <div className="flex items-center gap-2 px-3 pb-2.5">
            {running && (
              <button
                onClick={() => stop()}
                className="px-2.5 py-1 rounded-lg border border-err/40 text-err text-xs hover:bg-err/10"
                title="Stop the current task"
              >
                ■ Stop
              </button>
            )}
            <span className="text-[11px] text-faint ml-1">
              Enter to send · Shift+Enter for newline
            </span>
            <div className="ml-auto flex rounded-lg overflow-hidden border border-line">
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
