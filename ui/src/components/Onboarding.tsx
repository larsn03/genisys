// First-run wizard (SPEC G13 cold-start < 2 min): no endpoints in
// config.yaml → ask for a base URL, verify it live, write config.yaml.
import { useState } from "react";
import { api, TOKEN } from "../api";
import { useStore } from "../store";

export function Onboarding() {
  const [url, setUrl] = useState("http://localhost:8080/v1");
  const [models, setModels] = useState<string[] | null>(null);
  const [model, setModel] = useState("");
  const [ctx, setCtx] = useState("262144");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const loadModels = useStore((s) => s.loadModels);

  const test = () => {
    setBusy(true);
    setErr("");
    setModels(null);
    fetch(`/api/endpoints/test-url?token=${encodeURIComponent(TOKEN)}`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${TOKEN}` },
      body: JSON.stringify({ base_url: url }),
    })
      .then(async (r) => {
        const j = await r.json();
        if (!r.ok || !j.ok) throw new Error(j.error || `HTTP ${r.status}`);
        setModels(j.models ?? []);
        if (j.models?.length) setModel(j.models[0]);
      })
      .catch((e) => setErr(String(e)))
      .finally(() => setBusy(false));
  };

  const save = async () => {
    setBusy(true);
    setErr("");
    const epID = "primary";
    const yaml = `# Genisys configuration — every setting lives here.
version: 1
default_model: ${epID}/${model}
endpoints:
  - id: ${epID}
    name: My endpoint
    base_url: ${url}
    models:
      - id: ${model}
        context_window: ${ctx || "8192"}
        supports:
          tools: native
`;
    try {
      await api.putConfig(yaml);
      await loadModels();
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="text-center">
      <div className="text-3xl mb-2">⚡</div>
      <div className="text-sm text-ink mb-1">Welcome to Genisys</div>
      <div className="text-[13px] text-mute mb-5 max-w-md">
        Point me at any OpenAI-compatible endpoint — llama.cpp, vLLM, Ollama,
        OpenRouter… Everything is stored in <span className="font-mono">~/.genisys/config.yaml</span>.
      </div>
      <div className="flex gap-2 max-w-md mx-auto">
        <input
          className="flex-1 bg-panel border border-line rounded-lg px-3 py-2 text-[13px] font-mono text-ink outline-none focus:border-accent"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          placeholder="http://192.168.1.10:8080/v1"
        />
        <button
          disabled={busy || !url}
          onClick={test}
          className="shrink-0 px-4 py-2 rounded-lg bg-accent text-white text-[13px] hover:opacity-90 disabled:opacity-40"
        >
          {busy ? "…" : "Test"}
        </button>
      </div>
      {models && (
        <div className="mt-4 max-w-md mx-auto text-left border border-line rounded-xl p-4 bg-panel/60">
          <div className="text-[12px] text-ok mb-2">✓ connected — {models.length} model(s)</div>
          <label className="block text-[11px] text-faint mb-1">model</label>
          <select
            className="w-full bg-bg border border-line rounded-lg px-2.5 py-1.5 text-[13px] font-mono text-ink mb-3"
            value={model}
            onChange={(e) => setModel(e.target.value)}
          >
            {models.map((m) => (
              <option key={m}>{m}</option>
            ))}
          </select>
          <label className="block text-[11px] text-faint mb-1">context window (tokens)</label>
          <input
            className="w-full bg-bg border border-line rounded-lg px-2.5 py-1.5 text-[13px] font-mono text-ink mb-3"
            value={ctx}
            onChange={(e) => setCtx(e.target.value)}
          />
          <button
            disabled={busy || !model}
            onClick={() => void save()}
            className="w-full px-4 py-2 rounded-lg bg-accent text-white text-[13px] hover:opacity-90 disabled:opacity-40"
          >
            Save & start chatting
          </button>
        </div>
      )}
      {err && <div className="mt-3 text-[12px] text-err max-w-md mx-auto break-all">{err}</div>}
    </div>
  );
}
