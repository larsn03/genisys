// Wire types (mirror of the Go server, SPEC §21).

export interface WSEvent {
  type: string;
  id: number;
  session_id: string;
  step_id?: string;
  payload?: Record<string, unknown>;
}

export interface SessionRow {
  id: string;
  project_id?: string;
  title: string;
  model_ref: string;
  status: string;
  pinned: boolean;
  updated_at: string;
  live?: boolean;
}

export interface MessageRow {
  id: number;
  role: string;
  content_json: string;
  step_id?: string;
  created_at: string;
}

export interface ToolChip {
  callId: string;
  tool: string;
  args: string;
  ok?: boolean;
  summary?: string;
  running: boolean;
}

export interface FileChip {
  path: string;
  change: string;
  diff?: string;
}

export type Item =
  | { kind: "user"; id: string; text: string }
  | { kind: "assistant"; id: string; step: string; text: string }
  | {
      kind: "step";
      id: string;
      step: string;
      startedAt: number;
      done: boolean;
      tools: ToolChip[];
      files: FileChip[];
      tokens?: number;
    }
  | {
      kind: "permission";
      id: string;
      permId: number;
      path: string;
      mode: string;
      reason: string;
      answered?: "yes" | "no";
    }
  | {
      kind: "interjection";
      id: string;
      ijId: string;
      text: string;
      mode: string;
      state: "pending" | "delivered";
    }
  | { kind: "error"; id: string; text: string };
