// REST client. Token comes from the URL (?token=) that `genisys serve`
// printed and opened.
export const TOKEN = new URLSearchParams(location.search).get("token") ?? "";
async function req(method, path, body) {
    const r = await fetch(path, {
        method,
        headers: {
            "Content-Type": "application/json",
            ...(TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {}),
        },
        body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!r.ok)
        throw new Error(`${method} ${path} → ${r.status}: ${await r.text()}`);
    return r.status === 204 ? undefined : (await r.json());
}
export const api = {
    sessions: () => req("GET", "/api/sessions"),
    createSession: (body) => req("POST", "/api/sessions", body),
    patchSession: (id, body) => req("PATCH", `/api/sessions/${id}`, body),
    archiveSession: (id) => req("DELETE", `/api/sessions/${id}`),
    messages: (id) => req("GET", `/api/sessions/${id}/messages`),
    prompt: (id, text, mode) => req("POST", `/api/sessions/${id}/prompt`, { text, mode }),
    stop: (id) => req("POST", `/api/sessions/${id}/stop`),
    answer: (id, permId, granted) => req("POST", `/api/sessions/${id}/permissions/${permId}`, { granted }),
    interjections: (id) => req("GET", `/api/sessions/${id}/interjections`),
    withdraw: (id, ij) => req("POST", `/api/sessions/${id}/interjections/${ij}/withdraw`),
    promote: (id, ij) => req("POST", `/api/sessions/${id}/interjections/${ij}/promote`),
    files: (id, path = "") => req("GET", `/api/sessions/${id}/files?path=${encodeURIComponent(path)}`),
    file: (id, path) => fetch(`/api/sessions/${id}/file?path=${encodeURIComponent(path)}`, {
        headers: { Authorization: `Bearer ${TOKEN}` },
    }).then((r) => (r.ok ? r.text() : Promise.reject(new Error(String(r.status))))),
    putFile: (id, path, content) => req("PUT", `/api/sessions/${id}/file?path=${encodeURIComponent(path)}`, { content }),
    checkpoints: (id) => req("GET", `/api/sessions/${id}/checkpoints`),
    revert: (id, stepId) => req("POST", `/api/sessions/${id}/revert`, { step_id: stepId }),
};
