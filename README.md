# Genisys

**A desktop AI agent for open models on your own hardware.**

Genisys is a faithful recreation of [LM Studio Bionic](docs/images/README.md)
— the desktop agent for open models — re-targeted at any
**OpenAI-compatible endpoint**: llama.cpp, vLLM, Ollama, LM Studio,
OpenRouter, or anything that speaks `POST /v1/chat/completions`. One static
binary. YAML-only configuration. No accounts, no telemetry, no cloud
required — your prompts go only where you point the URL.

Everything that made Bionic *Bionic* is here: projects and sessions, a real
workspace with **checkpointed** file changes you can undo per step or per
file, a scratchpad, document generation (PDF / DOCX / XLSX / PPTX) from an
**embedded sandboxed Python**, skills, MCP servers, sub-agent search, and
the signature **interjection** model — you can steer or queue messages
*while the agent is working*.

> Specification: [`docs/SPEC.md`](docs/SPEC.md) ·
> Build plan: [`docs/IMPLEMENTATION.md`](docs/IMPLEMENTATION.md)

---

## Table of contents

1. [Install](#install)
2. [First run](#first-run)
3. [The interface](#the-interface)
4. [Talking to the agent](#talking-to-the-agent)
5. [Files, checkpoints and undo](#files-checkpoints-and-undo)
6. [Documents & attachments](#documents--attachments)
7. [Skills, MCP, web search](#skills-mcp-web-search)
8. [Settings](#settings)
9. [Command line](#command-line)
10. [Configuration](#configuration)
11. [FAQ](#faq)
12. [Build from source](#build-from-source)

---

## Install

### Linux (primary target — one file, nothing else)

Grab the AppImage from the
[releases page](https://github.com/larsn03/genisys/releases):

```bash
chmod +x Genisys-*-x86_64.appimage
./Genisys-*-x86_64.appimage
```

That starts Genisys, prints a tokenized URL, and opens your browser. The
AppImage contains *everything*: the server, the web UI, and a sandboxed
CPython for document generation. It runs on any glibc distro (Ubuntu LTS,
Fedora, Arch) with no other installs.

### Any OS (raw binary)

Binaries and tarballs for `linux/amd64`, `linux/arm64`, `darwin/arm64` and
`windows/amd64` are attached to each release:

```bash
./genisys serve        # starts the server and opens the browser
```

macOS/Windows installers are planned; the binaries work fine today.

---

## First run

Genisys opens straight into **onboarding**:

1. **Point me at your model** — paste a base URL, e.g.
   `http://192.168.178.84:8080/v1` or `http://localhost:11434/v1`, and hit
   **Test**. Genisys lists the models that endpoint actually serves.
2. **Pick a model** and set its context window (the endpoint usually
   reports it; adjust if needed).
3. **Save & start chatting.**

Everything you just configured lives in one human-editable file:
`~/.genisys/config.yaml`. There is no other settings store.

---

## The interface

Three panes, like Bionic:

```
┌────────────┬───────────────────────────────┬───────────────────────┐
│ LEFT RAIL  │  SESSION PANE (chat)          │  RIGHT PANEL (tabs)   │
│ projects & │  your messages, assistant     │  Files · Editor ·     │
│ sessions   │  replies, step traces, diff   │  Browser · History    │
│ (pin/fork/ │  cards, tool chips            │                       │
│  rename/…) │  ─────────────────────────    │  preview overlays,    │
│            │  composer: @files @skills,    │  Monaco editor,       │
│  ⚙ gear    │  attachments, model chip,     │  in-app browser       │
│  settings  │  context meter                │                       │
└────────────┴───────────────────────────────┴───────────────────────┘
```

- **Left rail** — sessions grouped by project. Right-click (or hover **⋯**)
  for **Pin · Rename · Fork · Archive · Delete**. A blue dot means the
  agent finished something in that session while you were away.
- **Chat pane** — every assistant turn shows a **step trace**: the tools it
  called, files it touched, tokens it spent. Each step has a **↺ Revert
  step** button. Failed turns become error cards with **Retry / Switch
  model / Edit last message / Compact now**.
- **Right panel** — the project's **Files** tree (with git status badges),
  a code **Editor**, a **Browser** tab the agent can open pages into, and
  **History** (checkpoints).

Sessions can be **forked** at any point — even mid-run — to try a different
direction without losing the original.

---

## Talking to the agent

### Interjections — the headline feature

While the agent is working you can still type. You choose what happens:

| Mode | What it does |
|---|---|
| **Queue** | Your message waits and *must* be read at the next tool boundary — nothing gets derailed. |
| **Interrupt** | The current step is cancelled (processes killed cleanly) and your instruction takes over immediately. |

Queued messages appear as chips you can **promote** (escalate to
interrupt) or **withdraw** before they're read. The default mode is a
setting (`agent.interjection.default_mode`); holding **Shift** while
sending inverts it for that one message.

### Getting around the workspace

- `@` in the composer opens a **file palette** — reference workspace files
  precisely.
- `@skill:name` explicitly invokes a **skill** (see below).
- 📎 attaches files (uploads go to the session scratchpad; the agent reads
  them with its own tools).
- The **model chip** switches models per session; the **context meter**
  shows how full the window is. At 80% Genisys compacts older history into
  a structured summary — you'll see a `⇲ Context compacted` marker, and
  context-overflow errors self-heal with a compact-and-retry.

### Projects

A **project** is just a folder on your machine. Create one from the rail
(+ New project), and the agent works directly in it — with your permission
for anything outside the granted paths.

---

## Files, checkpoints and undo

Every mutation the agent makes to your workspace is **checkpointed before
it happens**, in a shadow git repo — your own `.git` is never touched.

- **Revert step** — one click on any step card rolls the workspace back to
  just before that step.
- **History tab** — the checkpoint ledger (labels like
  `before replace_file(index.html)`).
- **Files → right-click → History & restore…** — per-file snapshot
  timeline with content preview; restore any file to any snapshot. If
  *you* edited the file since the last checkpoint, Genisys warns you
  before overwriting your work.
- Undo of an undo works too — restores are themselves checkpointed.

CLI escape hatch: `genisys revert --session <id>`.

---

## Documents & attachments

In **work** projects the agent acts as a document craftsman:

- Attach proposals/notes (📎), ask for a *decision memo with comparison
  table* → you get a markdown file delivered into the workspace, sources
  cited.
- Ask for a *6-slide deck from these notes* → a real `.pptx`, generated by
  an **embedded CPython** running in a WASM sandbox (no system Python, no
  pip, nothing to install). PDF, DOCX (with tracked redlines) and XLSX
  work the same way.
- Generated files appear as **delivery cards** — click to preview
  (markdown/PDF/images natively; Office docs via LibreOffice when
  installed, with a graceful download fallback when not).
- The agent states **assumptions** explicitly in work deliverables.

---

## Skills, MCP, web search

### Skills

A skill is a folder with a `SKILL.md` (YAML frontmatter + instructions) —
reusable know-how the agent reads on demand. Two are built in
(`skill-management`, `documents-presentations-spreadsheets`, plus
`introspection` and `file-history`), and you can:

- install one from a URL in **Settings → Skills**,
- drop folders into `~/.genisys/skills/` or `<project>/skills/`,
- invoke one explicitly with `@skill:name` in the composer.

Claude Code / Codex skill folders work unmodified.

### MCP servers

Add servers under `mcp_servers:` in `config.yaml` (stdio or HTTP). MCP
tools never bloat the prompt — the agent discovers them lazily
(`get_mcp_info` → `get_mcp_tools_info` → `call_mcp_tool`), and any
non-read-only MCP call asks you for permission first.

### Web search

Default backend is **SearXNG** (any instance you run — private, no
third-party API). Tavily and Brave are supported via env-var tokens.
`web_search` results are wrapped as untrusted content; `web_extract`
fetches pages into clean markdown. The in-app **Browser** tab only embeds
localhost and origins you allow-list in Settings → Browser.

---

## Settings

The ⚙ gear opens the settings screens — every one of them writes
`config.yaml` directly (comments in your file are preserved when Genisys
edits it):

| Tab | What's there |
|---|---|
| **Models** | default model, endpoints with **Test connection**, context windows |
| **Agent** | max steps, shell timeout, interjection default, theme (light / dark / system) |
| **Web** | provider, SearXNG URL, live **Probe** |
| **Browser** | allowed origins |
| **Skills** | list / install / remove |
| **MCP** | configured servers (edit in YAML) |
| **YAML** | the raw config file with validate-before-save |

And you can usually skip settings entirely: just tell the agent
*“set max steps to 50”* or *“what model are you?”* — it edits its own
config through `genisys_tool`.

---

## Command line

One binary, several faces:

```bash
genisys serve                     # web UI (default command)
genisys serve --addr 0.0.0.0:8710 # expose on your LAN (token still required)
genisys serve --no-open           # don't auto-open the browser

genisys run                       # terminal REPL in the current folder
genisys run --cwd ~/code/app "fix the failing tests"   # one-shot
genisys run --resume latest       # continue the last session here
#   inside the REPL: /steer <text> interrupts · /queue <text> waits

genisys revert --session <id>     # roll workspace back
genisys search test               # probe the configured web backend
genisys config show | validate    # inspect config.yaml
genisys version                   # version + git commit
```

---

## Configuration

`~/.genisys/config.yaml` is the only configuration file (full schema:
SPEC §18). A minimal working file:

```yaml
version: 1
default_model: home/swift-1.5-iq3_s
endpoints:
  - id: home
    name: Home LAN
    base_url: http://192.168.178.84:8080/v1
    models:
      - id: swift-1.5-iq3_s
        context_window: 262144
        supports:
          tools: native     # native | xml | none
          vision: true
```

Rules of the house:

- **YAML for everything configurable.** JSON is only a wire format; SQLite
  only runtime state.
- **No secrets in YAML** — API keys come from env vars (`api_key_env`).
- Projects can override agent settings via `<project>/project.yaml`;
  sessions via `session.yaml`.
- The server binds `127.0.0.1` and requires a bearer **token** on every
  API call — the URL it opens already carries it.

State lives under `~/.genisys/` (SQLite ledger, transcripts, scratchpads,
checkpoints). Deleting a session deletes its data.

---

## FAQ

**Is it a desktop app or a website?**
Both, sort of: it installs and launches like a desktop app (AppImage,
desktop entry, icon) and opens its UI in a browser tab served from
localhost. A native-window shell (Wails/Tauri-style) is a tracked future
step (SPEC Q1).

**Does anything leave my machine?**
Only what your configured endpoint receives. Web search defaults to your
own SearXNG instance; with no endpoint configured, nothing happens at all.

**Why does it need a token in the URL?**
The server is a local HTTP server; the token keeps other browser tabs (or
other machines, if you bind `0.0.0.0`) from talking to it.

**Can I run two agents at once?**
Yes — up to 4 concurrent sessions; more queue automatically.

**What can it *not* do (yet)?**
Voice dictation, local model management (that's your endpoint's job), and
native window packaging (see FAQ #1). macOS/Windows installers are
planned.

---

## Build from source

Requirements: Go ≥ 1.23, Node ≥ 20 (only to build the UI once — `ui/dist`
is committed so plain `go build` works without Node).

```bash
make ui        # node build → ui/dist (embedded into the binary via go:embed)
make build     # CGO off → ./genisys
make testrace  # full race test suite
make e2e       # Playwright browser tests against a scripted mock endpoint
make dist      # 4-OS tarballs + AppImage (needs appimagetool)
```

---

## License

TBD.
