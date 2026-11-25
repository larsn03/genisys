// REST client. Token comes from the URL (?token=) that `genisys serve`
// printed and opened.

export const TOKEN = new URLSearchParams(location.search).get("token") ?? "";

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const r = await fetch(path, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!r.ok) throw new Error(`${method} ${path} → ${r.status}: ${await r.text()}`);
  return r.status === 204 ? (undefined as T) : ((await r.json()) as T);
}

export const api = {
  sessions: () => req<import("./types").SessionRow[]>("GET", "/api/sessions"),
  createSession: (body: Record<string, unknown>) =>
    req<import("./types").SessionRow>("POST", "/api/sessions", body),
  patchSession: (id: string, body: Record<string, unknown>) =>
    req<import("./types").SessionRow>("PATCH", `/api/sessions/${id}`, body),
  archiveSession: (id: string) => req<{ ok: boolean }>("DELETE", `/api/sessions/${id}`),
  messages: (id: string) => req<import("./types").MessageRow[]>("GET", `/api/sessions/${id}/messages`),
  prompt: (id: string, text: string, mode?: string) =>
    req<{ state: string }>("POST", `/api/sessions/${id}/prompt`, { text, mode }),
  stop: (id: string) => req<{ stopped: boolean }>("POST", `/api/sessions/${id}/stop`),
  answer: (id: string, permId: number, granted: boolean) =>
    req<{ ok: boolean }>("POST", `/api/sessions/${id}/permissions/${permId}`, { granted }),
  interjections: (id: string) =>
    req<{ ID: string; Text: string; Mode: string }[]>("GET", `/api/sessions/${id}/interjections`),
  withdraw: (id: string, ij: string) =>
    req<{ ok: boolean }>("POST", `/api/sessions/${id}/interjections/${ij}/withdraw`),
  promote: (id: string, ij: string) =>
    req<{ ok: boolean }>("POST", `/api/sessions/${id}/interjections/${ij}/promote`),
  files: (id: string, path = "") =>
    req<{ name: string; is_dir: boolean; size: number }[]>("GET", `/api/sessions/${id}/files?path=${encodeURIComponent(path)}`),
  file: (id: string, path: string) =>
    fetch(`/api/sessions/${id}/file?path=${encodeURIComponent(path)}`, {
      headers: { Authorization: `Bearer ${TOKEN}` },
    }).then((r) => (r.ok ? r.text() : Promise.reject(new Error(String(r.status))))),
  putFile: (id: string, path: string, content: string) =>
    req<{ ok: boolean }>("PUT", `/api/sessions/${id}/file?path=${encodeURIComponent(path)}`, { content }),
  checkpoints: (id: string) =>
    req<{ id: number; step_id: string; sha: string; label: string; created_at: string }[]>(
      "GET",
      `/api/sessions/${id}/checkpoints`,
    ),
  revert: (id: string, stepId: string) =>
    req<{ reverted: string[] }>("POST", `/api/sessions/${id}/revert`, { step_id: stepId }),
};
