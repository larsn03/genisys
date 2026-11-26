// Settings screens (SPEC §5.5): every field writes config.yaml through
// PATCH (dotted key → comment-preserving edit) or PUT (raw YAML). No
// hidden state: what the screen shows is what the file contains.
import { useEffect, useState } from "react";
import { api, TOKEN, type Settings } from "../api";
import { useStore } from "../store";

type Tab = "models" | "agent" | "web" | "browser" | "skills" | "mcp" | "yaml";

const tabs: { id: Tab; label: string }[] = [
  { id: "models", label: "Models" },
  { id: "agent", label: "Agent" },
  { id: "web", label: "Web" },
  { id: "browser", label: "Browser" },
  { id: "skills", label: "Skills" },
  { id: "mcp", label: "MCP" },
  { id: "yaml", label: "YAML" },
];

export function SettingsModal() {
  const open = useStore((s) => s.settingsOpen);
  const setOpen = useStore((s) => s.setSettingsOpen);
  const [tab, setTab] = useState<Tab>("models");
  const [cfg, setCfg] = useState<Settings | null>(null);
  const [msg, setMsg] = useState("");

  useEffect(() => {
    if (!open) return;
    setMsg("");
    void api.settings().then(setCfg).catch((e) => setMsg(String(e)));
  }, [open]);

  if (!open) return null;

  // resolves to null on success, the error text otherwise — tabs show
  // it inline right where the user clicked, not just in the footer
  const patch = async (kv: Record<string, string>, note = "saved ✓"): Promise<string | null> => {
    setMsg("");
    try {
      await api.patchConfig(kv);
      setCfg(await api.settings());
      setMsg(note);
      return null;
    } catch (e) {
      setMsg(String(e));
      return String(e);
    }
  };

  return (
    <div
      className="fixed inset-0 z-40 bg-black/50 flex items-center justify-center p-6"
      onClick={() => setOpen(false)}
    >
      <div
        className="w-full max-w-4xl max-h-[85vh] bg-panel border border-line rounded-2xl shadow-2xl flex flex-col overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-1 px-4 py-3 border-b border-line">
          <span className="text-sm text-ink mr-3">⚙ Settings</span>
          {tabs.map((t) => (
            <button
              key={t.id}
              onClick={() => setTab(t.id)}
              className={`text-[12px] px-2.5 py-1 rounded-lg ${
                tab === t.id ? "bg-panel2 text-ink" : "text-mute hover:text-ink"
              }`}
            >
              {t.label}
            </button>
          ))}
          <button onClick={() => setOpen(false)} className="ml-auto text-faint hover:text-ink">
            ✕
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-5 text-[13px]">
          {!cfg ? (
            <div className="text-mute">{msg || "loading…"}</div>
          ) : tab === "models" ? (
            <ModelsTab cfg={cfg} patch={patch} />
          ) : tab === "agent" ? (
            <AgentTab cfg={cfg} patch={patch} />
          ) : tab === "web" ? (
            <WebTab cfg={cfg} patch={patch} />
          ) : tab === "browser" ? (
            <BrowserTab cfg={cfg} patch={patch} />
          ) : tab === "skills" ? (
            <SkillsTab />
          ) : tab === "mcp" ? (
            <MCPTab cfg={cfg} />
          ) : (
            <YamlTab onSaved={() => void api.settings().then(setCfg)} />
          )}
        </div>

        {msg && (
          <div className="px-5 py-2.5 border-t border-line text-[12px] text-accent2">{msg}</div>
        )}
      </div>
    </div>
  );
}

const field =
  "bg-bg border border-line rounded-lg px-2.5 py-1.5 text-[13px] text-ink outline-none focus:border-accent w-full";
const label = "text-[11px] text-faint uppercase tracking-wide";

function Row({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[220px_1fr] gap-4 items-center py-2 border-b border-line/40">
      <div className={label}>{k}</div>
      <div>{children}</div>
    </div>
  );
}

function NumRow({ k, path, value, patch }: { k: string; path: string; value: number; patch: P }) {
  const [v, setV] = useState(String(value));
  return (
    <Row k={k}>
      <div className="flex gap-2">
        <input className={field} value={v} onChange={(e) => setV(e.target.value)} />
        <button
          className="shrink-0 text-[12px] px-3 py-1.5 rounded-lg border border-line text-mute hover:text-ink"
          onClick={() => void patch({ [path]: v })}
        >
          Save
        </button>
      </div>
    </Row>
  );
}

type P = (kv: Record<string, string>, note?: string) => Promise<string | null>;

function ModelsTab({ cfg, patch }: { cfg: Settings; patch: P }) {
  const models = cfg.endpoints.flatMap((ep) =>
    ep.models.map((m) => ({ ref: `${ep.id}/${m.id}`, ep: ep.name || ep.id, ...m })),
  );
  const [test, setTest] = useState<Record<string, string>>({});
  const runTest = (id: string) => {
    setTest((t) => ({ ...t, [id]: "testing…" }));
    fetch(`/api/endpoints/${id}/test?token=${encodeURIComponent(TOKEN)}`, {
      method: "POST",
      headers: { Authorization: `Bearer ${TOKEN}` },
    })
      .then(async (r) => {
        const j = await r.json();
        setTest((t) => ({
          ...t,
          [id]: j.ok
            ? `✓ ${(j.models ?? []).length} model(s): ${(j.models ?? []).slice(0, 4).join(", ")}`
            : `✗ ${j.error}`,
        }));
      })
      .catch((e) => setTest((t) => ({ ...t, [id]: `✗ ${e}` })));
  };
  return (
    <div>
      <Row k="Default model">
        <select
          className={field}
          value={cfg.default_model}
          onChange={(e) => void patch({ default_model: e.target.value }, `default → ${e.target.value}`)}
        >
          {models.map((m) => (
            <option key={m.ref} value={m.ref}>
              {m.ref}
            </option>
          ))}
        </select>
      </Row>
      <div className="mt-4">
        {cfg.endpoints.map((ep) => (
          <div key={ep.id} className="mb-3 border border-line rounded-xl p-3">
            <div className="flex items-center gap-2 text-[13px]">
              <span className="text-ink font-medium">{ep.name || ep.id}</span>
              <span className="font-mono text-[11px] text-mute">{ep.base_url}</span>
              {ep.api_key_env && (
                <span className="text-[11px] text-faint">key: ${ep.api_key_env}</span>
              )}
              <button
                className="ml-auto text-[11px] px-2 py-1 rounded-lg border border-line text-mute hover:text-ink"
                onClick={() => runTest(ep.id)}
              >
                Test connection
              </button>
            </div>
            {test[ep.id] && (
              <div className={`mt-1 text-[11px] font-mono ${test[ep.id].startsWith("✓") ? "text-ok" : "text-err"}`}>
                {test[ep.id]}
              </div>
            )}
            <table className="w-full mt-2 text-[12px]">
              <thead className="text-faint text-left">
                <tr>
                  <th className="font-normal">model</th>
                  <th className="font-normal">context</th>
                  <th className="font-normal">tools</th>
                  <th className="font-normal">vision</th>
                </tr>
              </thead>
              <tbody className="font-mono">
                {ep.models.map((m) => (
                  <tr key={m.id} className="border-t border-line/40">
                    <td className="py-1 text-ink">{m.id}</td>
                    <td>{(m.context_window / 1024).toFixed(0)}k</td>
                    <td>{m.tools || "—"}</td>
                    <td>{m.vision ? "yes" : "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ))}
        <p className="text-[12px] text-faint">
          Endpoints are lists in config.yaml — add or edit them in the YAML tab.
        </p>
      </div>
    </div>
  );
}

function AgentTab({ cfg, patch }: { cfg: Settings; patch: P }) {
  return (
    <div>
      <NumRow k="Max steps" path="agent.max_steps" value={cfg.agent.max_steps} patch={patch} />
      <NumRow
        k="Shell timeout (ms)"
        path="agent.shell_timeout_ms"
        value={cfg.agent.shell_timeout_ms}
        patch={patch}
      />
      <Row k="Interjection default">
        <select
          className={field}
          value={cfg.agent.interjection.default_mode}
          onChange={(e) =>
            void patch({ "agent.interjection.default_mode": e.target.value }, `default mode → ${e.target.value}`)
          }
        >
          <option value="queue">queue — read at the next boundary</option>
          <option value="interrupt">interrupt — steer immediately</option>
        </select>
      </Row>
      <Row k="Theme">
        <select
          className={field}
          value={cfg.ui.theme}
          onChange={(e) =>
            void patch({ "ui.theme": e.target.value }, `theme → ${e.target.value}`).then(() =>
              useStore.getState().applyTheme(),
            )
          }
        >
          <option value="dark">dark</option>
          <option value="light">light</option>
          <option value="system">system</option>
        </select>
      </Row>
      <Row k="Subagent model">
        <input
          className={field}
          placeholder="(same as session)"
          defaultValue={cfg.agent.subagent_model}
          onKeyDown={(e) =>
            e.key === "Enter" &&
            void patch({ "agent.subagent_model": (e.target as HTMLInputElement).value })
          }
        />
      </Row>
      <p className="text-[12px] text-faint mt-3">
        The agent can change most of this for you — just ask (“set max steps to 50”).
      </p>
    </div>
  );
}

function WebTab({ cfg, patch }: { cfg: Settings; patch: P }) {
  const [hint, setHint] = useState("");
  const [probe, setProbe] = useState("");
  const [baseUrl, setBaseUrl] = useState(cfg.web.searxng.base_url);
  return (
    <div>
      <Row k="Search enabled">
        <select
          className={field}
          value={cfg.web.search_enabled ? "on" : "off"}
          onChange={(e) =>
            void patch({ "web.search_enabled": e.target.value === "on" ? "true" : "false" }).then((err) =>
              setHint(err ? err.replace(/^.*?\.yaml:\s*/, "") : "")
            )
          }
        >
          <option value="off">off</option>
          <option value="on">on</option>
        </select>
      </Row>
      {hint && (
        <div className="text-[11.5px] text-accent2 pb-1 pl-[236px]">{hint}</div>
      )}
      <Row k="Provider">
        <select
          className={field}
          value={cfg.web.provider}
          onChange={(e) => void patch({ "web.provider": e.target.value })}
        >
          <option value="searxng">SearXNG (default, private)</option>
          <option value="tavily">Tavily</option>
          <option value="brave">Brave</option>
          <option value="none">none</option>
        </select>
      </Row>
      <Row k="SearXNG base URL">
        <div className="flex gap-2">
          <input className={field} value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />
          <button
            className="shrink-0 text-[12px] px-3 py-1.5 rounded-lg border border-line text-mute hover:text-ink"
            onClick={() => void patch({ "web.searxng.base_url": baseUrl }).then((err) => setHint(err ?? ""))}
          >
            Save
          </button>
        </div>
      </Row>
      <Row k="Language">
        <input
          className={field}
          defaultValue={cfg.web.searxng.language}
          onKeyDown={(e) =>
            e.key === "Enter" &&
            void patch({ "web.searxng.language": (e.target as HTMLInputElement).value })
          }
        />
      </Row>
      <Row k="Test backend">
        <button
          className="text-[12px] px-3 py-1.5 rounded-lg border border-accent text-accent hover:bg-accent/10"
          onClick={() =>
            void api
              .webProbe()
              .then((r) => setProbe(r.enabled ? (r.probe ?? "?") : (r.message ?? "disabled")))
              .catch((e) => setProbe(String(e)))
          }
        >
          Probe
        </button>
        {probe && <div className="mt-2 text-[12px] text-mute break-all">{probe}</div>}
      </Row>
      <p className="text-[12px] text-faint mt-3">
        Tavily/Brave tokens are read from env vars (api_key_env) — Genisys never stores secrets in YAML.
      </p>
    </div>
  );
}

function BrowserTab({ cfg, patch }: { cfg: Settings; patch: P }) {
  const [origins, setOrigins] = useState(cfg.browser.allowed_origins.join("\n"));
  return (
    <div>
      <Row k="Allowed origins">
        <div className="flex gap-2 items-start">
          <textarea
            className={`${field} font-mono h-28`}
            value={origins}
            onChange={(e) => setOrigins(e.target.value)}
            placeholder={"https://example.com\nhttp://localhost:3000"}
          />
          <button
            className="shrink-0 text-[12px] px-3 py-1.5 rounded-lg border border-line text-mute hover:text-ink"
            onClick={() => void patch({ "browser.allowed_origins": origins.replace(/\n/g, ",") })}
          >
            Save
          </button>
        </div>
      </Row>
      <p className="text-[12px] text-faint mt-3">
        open_url_in_app_browser only embeds localhost, file://, and these origins.
      </p>
    </div>
  );
}

function SkillsTab() {
  const skills = useStore((s) => s.skills);
  const [url, setUrl] = useState("");
  const [msg, setMsg] = useState("");
  const loadSkills = useStore((s) => s.loadSkills);
  useEffect(() => {
    loadSkills();
  }, [loadSkills]);
  return (
    <div>
      {skills.map((sk) => (
        <div key={sk.name} className="flex items-center gap-3 py-2 border-b border-line/40">
          <div className="flex-1 min-w-0">
            <div className="text-ink">
              {sk.name}{" "}
              {sk.builtin && <span className="text-[10px] text-faint">built-in</span>}
            </div>
            <div className="text-[12px] text-mute truncate">{sk.description}</div>
          </div>
          {!sk.builtin && (
            <button
              className="text-[12px] text-err hover:underline"
              onClick={() =>
                void api
                  .deleteSkill(sk.name)
                  .then(loadSkills)
                  .catch((e) => setMsg(String(e)))
              }
            >
              Remove
            </button>
          )}
        </div>
      ))}
      <div className="flex gap-2 mt-4">
        <input
          className={field}
          placeholder="Install from URL (raw SKILL.md)"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
        />
        <button
          className="shrink-0 text-[12px] px-3 py-1.5 rounded-lg border border-accent text-accent hover:bg-accent/10"
          onClick={() =>
            void api
              .installSkill({ url })
              .then(() => {
                setUrl("");
                loadSkills();
              })
              .catch((e) => setMsg(String(e)))
          }
        >
          Install
        </button>
      </div>
      {msg && <div className="mt-2 text-[12px] text-warn">{msg}</div>}
      <p className="text-[12px] text-faint mt-3">
        Invoke a skill explicitly with @skill:name in the composer.
      </p>
    </div>
  );
}

function MCPTab({ cfg }: { cfg: Settings }) {
  return (
    <div>
      {cfg.mcp_servers.length === 0 && (
        <div className="text-mute">
          No MCP servers configured. Add entries under mcp_servers: in the YAML tab.
        </div>
      )}
      {cfg.mcp_servers.map((m) => (
        <div key={m.name} className="flex items-center gap-3 py-2 border-b border-line/40">
          <span className={`w-2 h-2 rounded-full ${m.enabled ? "bg-ok" : "bg-line"}`} />
          <span className="text-ink">{m.name}</span>
          <span className="font-mono text-[11px] text-mute truncate">
            {m.transport === "http" ? m.url : [m.command, ...m.args].join(" ")}
          </span>
          <span className="ml-auto text-[11px] text-faint">{m.transport}</span>
        </div>
      ))}
      <p className="text-[12px] text-faint mt-3">
        Servers start lazily on first use and idle out after 5 minutes.
      </p>
    </div>
  );
}

function YamlTab({ onSaved }: { onSaved: () => void }) {
  const [yaml, setYaml] = useState("");
  const [err, setErr] = useState("");
  const [dirty, setDirty] = useState(false);
  useEffect(() => {
    void api.configYAML().then(setYaml).catch((e) => setErr(String(e)));
  }, []);
  return (
    <div className="h-full flex flex-col gap-2">
      <textarea
        className={`${field} font-mono text-[12px] flex-1 min-h-[45vh] leading-relaxed`}
        value={yaml}
        onChange={(e) => {
          setYaml(e.target.value);
          setDirty(true);
        }}
        spellCheck={false}
      />
      {err && <div className="text-[12px] text-err whitespace-pre-wrap">{err}</div>}
      <div className="flex gap-2 justify-end">
        <button
          disabled={!dirty}
          className="text-[12px] px-3 py-1.5 rounded-lg border border-accent text-accent hover:bg-accent/10 disabled:opacity-40"
          onClick={() =>
            void api
              .putConfig(yaml)
              .then(() => {
                setErr("");
                setDirty(false);
                onSaved();
              })
              .catch((e) => setErr(String(e)))
          }
        >
          Validate & save
        </button>
      </div>
    </div>
  );
}
