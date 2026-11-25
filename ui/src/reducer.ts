// Pure event-stream reducer (SPEC §21): conversation items are derived
// from the ledger, so replay and live stream produce identical state.
// Unit-tested in reducer.test.ts.

import type { Item, WSEvent } from "./types";

let seq = 0;
const nid = () => `it_${++seq}_${Date.now()}`;

export function resetIds() {
  seq = 0;
}

type Payload = Record<string, unknown>;
const str = (p: Payload | undefined, k: string) => String(p?.[k] ?? "");

function openStep(items: Item[], step: string): Item | null {
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    if (it.kind === "step" && it.step === step) return it;
    if (it.kind === "step") return null;
  }
  return null;
}

function ensureStep(items: Item[], step: string): Item {
  const found = openStep(items, step);
  if (found) return found;
  const it: Item = {
    kind: "step",
    id: nid(),
    step,
    startedAt: Date.now(),
    done: false,
    tools: [],
    files: [],
  };
  items.push(it);
  return it;
}

function closeSteps(items: Item[]) {
  for (const it of items) if (it.kind === "step") it.done = true;
}

export function reduceEvent(items: Item[], ev: WSEvent): Item[] {
  const p = ev.payload as Payload | undefined;
  const step = str(p, "step") || ev.step_id || "";
  const out = items.slice();

  switch (ev.type) {
    case "user_turn": {
      closeSteps(out);
      out.push({ kind: "user", id: nid(), text: str(p, "text") });
      break;
    }
    case "step_started": {
      ensureStep(out, step);
      break;
    }
    case "step_finished": {
      const s = openStep(out, step);
      if (s && s.kind === "step") s.done = true;
      break;
    }
    case "text_delta": {
      // assistant prose streams as its own bubble after the step trace
      out.push({ kind: "assistant", id: nid(), step, text: str(p, "text") });
      break;
    }
    case "tool_started": {
      const s = ensureStep(out, step);
      if (s.kind === "step")
        s.tools.push({
          callId: str(p, "call_id"),
          tool: str(p, "tool"),
          args: str(p, "args"),
          running: true,
        });
      break;
    }
    case "tool_finished": {
      for (let i = out.length - 1; i >= 0; i--) {
        const it = out[i];
        if (it.kind !== "step") continue;
        const chip = it.tools.find((t) => t.callId === str(p, "call_id"));
        if (chip) {
          chip.running = false;
          chip.ok = Boolean(p?.ok);
          chip.summary = str(p, "summary");
          break;
        }
      }
      break;
    }
    case "file_changed": {
      const s = ensureStep(out, step || "_");
      if (s.kind === "step")
        s.files.push({
          path: str(p, "path"),
          change: str(p, "change"),
          diff: str(p, "diff"),
        });
      break;
    }
    case "usage": {
      const s = openStep(out, step);
      if (s && s.kind === "step")
        s.tokens = Number(p?.completion_tokens ?? 0) || s.tokens;
      break;
    }
    case "permission_requested": {
      out.push({
        kind: "permission",
        id: nid(),
        permId: Number(p?.id ?? 0),
        path: str(p, "path"),
        mode: str(p, "mode"),
        reason: str(p, "reason"),
      });
      break;
    }
    case "permission_answered": {
      for (let i = out.length - 1; i >= 0; i--) {
        const it = out[i];
        if (it.kind === "permission" && it.permId === Number(p?.id)) {
          it.answered = p?.granted ? "yes" : "no";
          break;
        }
      }
      break;
    }
    case "interjection_queued": {
      out.push({
        kind: "interjection",
        id: nid(),
        ijId: str(p, "id"),
        text: str(p, "text"),
        mode: str(p, "mode"),
        state: "pending",
      });
      break;
    }
    case "interjection_delivered": {
      for (let i = out.length - 1; i >= 0; i--) {
        const it = out[i];
        if (it.kind === "interjection" && it.ijId === str(p, "id")) {
          it.state = "delivered";
          break;
        }
      }
      break;
    }
    case "session_error": {
      closeSteps(out);
      out.push({ kind: "error", id: nid(), text: str(p, "error") });
      break;
    }
    case "session_status": {
      if (str(p, "status") === "idle" || str(p, "status") === "error")
        closeSteps(out);
      break;
    }
    default:
      // checkpoint_created, ui_* frames, interrupt_started… render inside
      // steps or later panels; ignore for the conversation spine.
      break;
  }
  return out;
}

// Rebuild items from persisted message rows (session switch, SPEC §6.3).
export function itemsFromMessages(rows: MessageRowLite[]): Item[] {
  const out: Item[] = [];
  for (const r of rows) {
    let m: { role: string; content?: string; tool_calls?: { id: string; function: { name: string; arguments: string } }[] };
    try {
      m = JSON.parse(r.content_json);
    } catch {
      continue;
    }
    if (m.role === "user" && m.content) {
      if (m.content.startsWith("[user interjection")) {
        out.push({
          kind: "interjection",
          id: nid(),
          ijId: "",
          text: m.content.replace(/^\[user interjection[^\]]*\]\s*/, ""),
          mode: "queue",
          state: "delivered",
        });
      } else if (!m.content.startsWith("[harness]")) {
        out.push({ kind: "user", id: nid(), text: m.content });
      }
    } else if (m.role === "assistant") {
      if (m.content)
        out.push({ kind: "assistant", id: nid(), step: r.step_id ?? "", text: m.content });
      if (m.tool_calls?.length) {
        out.push({
          kind: "step",
          id: nid(),
          step: r.step_id ?? "",
          startedAt: 0,
          done: true,
          tools: m.tool_calls.map((tc) => ({
            callId: tc.id,
            tool: tc.function.name,
            args: tc.function.arguments,
            running: false,
            ok: true,
          })),
          files: [],
        });
      }
    }
  }
  return out;
}

export interface MessageRowLite {
  role: string;
  content_json: string;
  step_id?: string;
}
