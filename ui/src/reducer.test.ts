// Event-reducer unit tests (P3-8 DoD): the same ledger replayed must
// produce the same conversation items as the live stream.

import { describe, expect, it, beforeEach } from "vitest";
import { itemsFromMessages, reduceEvent, resetIds } from "./reducer";
import type { Item, WSEvent } from "./types";

let n = 0;
const ev = (type: string, payload: Record<string, unknown> = {}): WSEvent => ({
  type,
  id: ++n,
  session_id: "s1",
  payload,
});

beforeEach(() => {
  resetIds();
  n = 0;
});

function all(items: Item[], kind: string) {
  return items.filter((i) => i.kind === kind);
}

describe("reduceEvent", () => {
  it("builds the canonical turn spine", () => {
    let items: Item[] = [];
    const ledger = [
      ev("user_turn", { text: "fix the bug" }),
      ev("session_status", { status: "running" }),
      ev("step_started", { step: "step_1", model: "m" }),
      ev("tool_started", { step: "step_1", call_id: "c1", tool: "read_file_lines", args: `{"path":"a.go"}` }),
      ev("file_changed", { step: "step_1", path: "a.go", change: "modified", diff: "+x\n-y" }),
      ev("tool_finished", { step: "step_1", call_id: "c1", ok: true, summary: "12 lines" }),
      ev("step_finished", { step: "step_1", stop_reason: "tool_use" }),
      ev("step_started", { step: "step_2", model: "m" }),
      ev("text_delta", { step: "step_2", text: "fixed it" }),
      ev("step_finished", { step: "step_2", stop_reason: "end_turn" }),
      ev("session_status", { status: "idle" }),
    ];
    for (const e of ledger) items = reduceEvent(items, e);

    expect(all(items, "user")).toHaveLength(1);
    expect(all(items, "assistant")).toHaveLength(1);
    const steps = all(items, "step");
    expect(steps).toHaveLength(2);
    const s1 = steps[0] as Extract<Item, { kind: "step" }>;
    expect(s1.done).toBe(true);
    expect(s1.tools[0]).toMatchObject({ tool: "read_file_lines", ok: true, running: false });
    expect(s1.files[0]).toMatchObject({ path: "a.go", change: "modified" });
  });

  it("replay is idempotent-identical to live ordering", () => {
    const ledger = [
      ev("user_turn", { text: "go" }),
      ev("step_started", { step: "step_1" }),
      ev("tool_started", { step: "step_1", call_id: "c", tool: "list_dir", args: "{}" }),
      ev("tool_finished", { step: "step_1", call_id: "c", ok: true, summary: "3 entries" }),
    ];
    let live: Item[] = [];
    for (const e of ledger) live = reduceEvent(live, e);
    resetIds();
    let replay: Item[] = [];
    for (const e of ledger) replay = reduceEvent(replay, e);
    expect(replay.map((i) => i.kind)).toEqual(live.map((i) => i.kind));
    expect(JSON.stringify(replay)).toBe(JSON.stringify(live));
  });

  it("tool_finished matches its chip by call_id across steps", () => {
    let items: Item[] = [];
    for (const e of [
      ev("step_started", { step: "step_1" }),
      ev("tool_started", { step: "step_1", call_id: "a", tool: "t1", args: "" }),
      ev("step_finished", { step: "step_1" }),
      ev("step_started", { step: "step_2" }),
      ev("tool_started", { step: "step_2", call_id: "b", tool: "t2", args: "" }),
      ev("tool_finished", { step: "step_2", call_id: "a", ok: false, summary: "late" }),
    ])
      items = reduceEvent(items, e);
    const steps = all(items, "step") as Extract<Item, { kind: "step" }>[];
    expect(steps[0].tools[0]).toMatchObject({ callId: "a", ok: false, running: false });
    expect(steps[1].tools[0].running).toBe(true);
  });

  it("permission cards answer in place", () => {
    let items: Item[] = [];
    for (const e of [
      ev("permission_requested", { id: 7, path: "/etc/x", mode: "readonly", reason: "r" }),
      ev("permission_answered", { id: 7, granted: true }),
    ])
      items = reduceEvent(items, e);
    const p = all(items, "permission")[0] as Extract<Item, { kind: "permission" }>;
    expect(p.answered).toBe("yes");
  });

  it("interjections flip to delivered", () => {
    let items: Item[] = [];
    for (const e of [
      ev("interjection_queued", { id: "ij_1", text: "stop", mode: "interrupt" }),
      ev("interjection_delivered", { id: "ij_1", boundary: "interrupt" }),
    ])
      items = reduceEvent(items, e);
    const ij = all(items, "interjection")[0] as Extract<Item, { kind: "interjection" }>;
    expect(ij.state).toBe("delivered");
  });

  it("interrupt_started renders the confirmation card", () => {
    let items: Item[] = [];
    for (const e of [
      ev("step_started", { step: "step_1" }),
      ev("interjection_queued", { id: "ij_1", text: "stop", mode: "interrupt" }),
      ev("interrupt_started", { step: "step_1" }),
      ev("interjection_delivered", { id: "ij_1", boundary: "interrupt" }),
    ])
      items = reduceEvent(items, e);
    expect(all(items, "interrupt")).toHaveLength(1);
    const ij = all(items, "interjection")[0] as Extract<Item, { kind: "interjection" }>;
    expect(ij.state).toBe("delivered");
  });

  it("context_compacted becomes a marker", () => {
    const items = reduceEvent([], ev("context_compacted", { reason: "threshold", before_tokens: 90000, after_tokens: 12000 }));
    const m = all(items, "marker")[0] as Extract<Item, { kind: "marker" }>;
    expect(m.text).toContain("Context compacted");
    expect(m.text).toContain("90,000");
    expect(m.text).toContain("12,000");
  });

  it("deliver + attach events become cards", () => {
    let items: Item[] = [];
    for (const e of [
      ev("ui_deliver_file", { path: "deck.pptx" }),
      ev("ui_attach_file", { path: "/scratch/attachments/memo.docx", alt: "" }),
    ])
      items = reduceEvent(items, e);
    const dels = all(items, "deliver") as Extract<Item, { kind: "deliver" }>[];
    expect(dels).toHaveLength(2);
    expect(dels[0]).toMatchObject({ path: "deck.pptx", attached: false });
    expect(dels[1].attached).toBe(true);
  });

  it("session_error closes open steps", () => {
    let items: Item[] = [];
    for (const e of [
      ev("step_started", { step: "step_1" }),
      ev("session_error", { error: "boom" }),
    ])
      items = reduceEvent(items, e);
    const s = all(items, "step")[0] as Extract<Item, { kind: "step" }>;
    expect(s.done).toBe(true);
    expect(all(items, "error")).toHaveLength(1);
  });
});

describe("itemsFromMessages", () => {
  it("rebuilds user/assistant/step items and hides harness noise", () => {
    const rows = [
      { role: "user", content_json: JSON.stringify({ role: "user", content: "hello" }) },
      {
        role: "assistant",
        content_json: JSON.stringify({
          role: "assistant",
          content: "",
          tool_calls: [{ id: "c", function: { name: "list_dir", arguments: "{}" } }],
        }),
        step_id: "step_1",
      },
      { role: "tool", content_json: JSON.stringify({ role: "tool", content: "3 entries" }) },
      {
        role: "user",
        content_json: JSON.stringify({ role: "user", content: "[harness] Step budget exhausted." }),
      },
      {
        role: "user",
        content_json: JSON.stringify({
          role: "user",
          content: "[user interjection — address this before continuing]\nactually use python",
        }),
      },
      { role: "assistant", content_json: JSON.stringify({ role: "assistant", content: "done" }) },
    ];
    const items = itemsFromMessages(rows);
    expect(items.map((i) => i.kind)).toEqual([
      "user",
      "step",
      "interjection",
      "assistant",
    ]);
  });
});
