# Genisys

A desktop AI agent for **open models on remote endpoints** — a faithful
recreation of [LM Studio Bionic](docs/images/README.md), re-targeted at any
OpenAI-compatible `chat/completions` server (llama.cpp, vLLM, Ollama,
OpenRouter, …). One static Go binary. YAML-only configuration. No telemetry,
no accounts, no cloud.

Full specification: [`docs/SPEC.md`](docs/SPEC.md) ·
implementation plan: [`docs/IMPLEMENTATION.md`](docs/IMPLEMENTATION.md)

## Install (Linux — primary target)

Download `Genisys-<version>-x86_64.appimage` from the
[latest release](https://github.com/larsn03/genisys/releases):

```bash
chmod +x Genisys-*-x86_64.appimage
./Genisys-*-x86_64.appimage
```

That's it: the AppImage starts `genisys serve`, prints a tokenized URL, and
opens your browser. Nothing else is installed — the web UI, a sandboxed
CPython (for document/spreadsheet/deck generation), and all tools are
embedded in the single file. Runs on any glibc distro (Ubuntu LTS, Fedora,
Arch).

Raw binaries and tarballs for `linux/amd64`, `linux/arm64`, `darwin/arm64`,
and `windows/amd64` are attached to each release (macOS/Windows packaging is
planned; the binaries work today: `./genisys serve`).

## First run

1. Genisys opens the onboarding screen — paste your endpoint base URL
   (e.g. `http://192.168.178.84:8080/v1`) and press **Test**.
2. Pick a model, set its context window, **Save**.
3. Chat. Attach files, point it at a project folder, interrupt or queue
   messages while it works.

Everything is stored in `~/.genisys/config.yaml` — the only config file,
hand-editable, comment-preserving when Genisys writes it:

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
          tools: native
          vision: true
```

The agent can edit its own config (`genisys_tool`), so "set max steps to 50"
works in chat.

## CLI

```bash
genisys serve            # web UI (default command)
genisys run              # terminal REPL on a project folder
genisys run --cwd ~/code/app "fix the failing tests"
genisys revert --session <id>   # roll workspace back
genisys search test      # probe the configured web backend
genisys config show      # print config.yaml
genisys version
```

## Highlights

- **Projects & sessions** with fork/pin/rename/archive, checkpointed
  workspace mutations (shadow git — your repo is never touched), per-file
  history & restore.
- **Interjections**: send messages while the agent works — *interrupt* to
  steer now, *queue* to make it read later.
- **Sandboxed Python** (embedded CPython-WASI) + stdlib document writers:
  PDF, DOCX redlines, XLSX, PPTX without system dependencies.
- **Skills** (`~/.genisys/skills`), **MCP servers**, **SearXNG web search**,
  in-app browser with an origin allow-list.
- **Context management**: result aging + auto-compaction at 80% with a
  visible "Context compacted" marker; overflow self-heals.

## Build from source

```bash
make ui      # web UI (node ≥ 20) → ui/dist, embedded via go:embed
make build   # CGO off → ./genisys
make testrace
make dist    # tarballs + AppImage (needs appimagetool)
```

## License

TBD.
