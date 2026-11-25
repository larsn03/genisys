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
  projects: () =>
    req<{ id: string; name: string; kind: string; workspace_path: string }[]>("GET", "/api/projects"),
  createProject: (body: Record<string, unknown>) =>
    req<{ id: string; name: string }>("POST", "/api/projects", body),
  upload: async (id: string, file: File) => {
    const fd = new FormData();
    fd.append("file", file);
    const r = await fetch(`/api/sessions/${id}/upload`, {
      method: "POST",
      headers: { Authorization: `Bearer ${TOKEN}` }, // FormData sets its own boundary
      body: fd,
    });
    if (!r.ok) throw new Error(`upload → ${r.status}: ${await r.text()}`);
    return (await r.json()) as { path: string; name: string; size: number };
  },
  createSession: (body: Record<string, unknown>) =>
    req<import("./types").SessionRow>("POST", "/api/sessions", body),
  patchSession: (id: string, body: Record<string, unknown>) =>
    req<import("./types").SessionRow>("PATCH", `/api/sessions/${id}`, body),
  archiveSession: (id: string) => req<{ ok: boolean }>("DELETE", `/api/sessions/${id}`),
  deleteSession: (id: string) => req<{ ok: boolean }>("DELETE", `/api/sessions/${id}?hard=1`),
  forkSession: (id: string, title?: string) =>
    req<import("./types").SessionRow>("POST", `/api/sessions/${id}/fork`, { title: title ?? "" }),
  compact: (id: string) => req<{ state: string }>("POST", `/api/sessions/${id}/compact`, {}),
  messages: (id: string) => req<import("./types").MessageRow[]>("GET", `/api/sessions/${id}/messages`),
  prompt: (id: string, text: string, mode?: string, skills?: string[]) =>
    req<{ state: string }>("POST", `/api/sessions/${id}/prompt`, { text, mode, skills }),
  installSkill: (body: { url?: string; content?: string }) =>
    req<{ name: string }>("POST", "/api/skills/install", body),
  deleteSkill: (name: string) =>
    req<unknown>("DELETE", `/api/skills/${encodeURIComponent(name)}`),
  skills: () =>
    req<{ skills: import("./types").SkillInfo[] }>("GET", "/api/skills"),
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
  models: () =>
    req<{ default: string; models: import("./types").ModelInfo[] }>("GET", "/api/models"),
  gitStatus: (id: string) =>
    req<import("./types").GitStatus>("GET", `/api/sessions/${id}/git-status`),
  fileHistory: (id: string, path: string) =>
    req<{ sha: string; label: string; when: string }[]>(
      "GET",
      `/api/sessions/${id}/file-history?path=${encodeURIComponent(path)}`,
    ),
  fileAtURL: (id: string, path: string, sha: string) =>
    `/api/sessions/${id}/file-at?path=${encodeURIComponent(path)}&sha=${sha}&token=${TOKEN}`,
  restoreFile: (id: string, path: string, sha: string, force = false) =>
    req<{ restored: string }>("POST", `/api/sessions/${id}/restore-file`, { path, sha, force }),
  settings: () => req<Settings>("GET", "/api/settings"),
  configYAML: async () => {
    const r = await fetch(`/api/config?token=${TOKEN}`, {
      headers: { Authorization: `Bearer ${TOKEN}` },
    });
    if (!r.ok) throw new Error(`config → ${r.status}`);
    return r.text();
  },
  putConfig: (yaml: string) => req<unknown>("PUT", "/api/config", { yaml }),
  patchConfig: (kv: Record<string, string>) =>
    req<unknown>("PATCH", "/api/config", kv),
  webProbe: () =>
    req<{ enabled: boolean; probe?: string; message?: string }>(
      "POST",
      "/api/web/probe",
    ),
  browserOrigins: () => req<{ origins: string[] }>("GET", "/api/browser/origins"),
  previewURL: (id: string, path: string) =>
    `/api/sessions/${id}/preview?path=${encodeURIComponent(path)}&token=${TOKEN}`,
  fileURL: (id: string, path: string) =>
    `/api/sessions/${id}/file?path=${encodeURIComponent(path)}&token=${TOKEN}`,
  checkpoints: (id: string) =>
    req<{ id: number; step_id: string; sha: string; label: string; created_at: string }[]>(
      "GET",
      `/api/sessions/${id}/checkpoints`,
    ),
  revert: (id: string, stepId: string) =>
    req<{ reverted: string[] }>("POST", `/api/sessions/${id}/revert`, { step_id: stepId }),
};

export interface Settings {
  default_model: string;
  agent: {
    max_steps: number;
    shell_timeout_ms: number;
    subagent_model: string;
    interjection: { default_mode: string; boundary: string };
  };
  web: {
    search_enabled: boolean;
    provider: string;
    searxng: { base_url: string; language: string; categories: string; timeout_ms: number };
    tavily: { api_key_env: string };
    brave: { api_key_env: string };
  };
  browser: { allowed_origins: string[] };
  skills: { import_from_other_apps: string[] };
  mcp_servers: { name: string; command: string; args: string[]; url: string; transport: string; enabled: boolean }[];
  endpoints: {
    id: string;
    name: string;
    base_url: string;
    api_key_env: string;
    models: { id: string; context_window: number; tools: string; vision: boolean }[];
  }[];
  ui: { theme: string };
}
