# Genisys — Implementation Plan

**Version:** 0.1 · **Date:** 2026-11-24 · **Companion to:** [`SPEC.md`](SPEC.md) (v0.2)

> **Progress (2026-11-25):** ✅ P0 · ✅ P1 · ✅ **P2 (all: P2-1 … P2-9)** · ✅ SearXNG
> web search (SPEC §9.5 made normative: remote instance support, JSON-API client,
> `genisys search test` probe, `web_search` tool — M4 item pulled forward) · ✅ P3a (server) + P3b (three-pane shell w/ resizable+persisted panes, event-reducer UI, step traces + per-step revert, model picker, @-palette, context meter, Monaco editor, git badges, checkpoint history tab, interjection promote/withdraw, interrupt card). 🟡 follow-ups: Playwright e2e job in CI, ProjectRail grouping, attachments (P4).
> Earlier milestone: durable
> sessions (sqlite + jsonl transcript + resume), enforced workspace permissions,
> shadow-git checkpoints with `genisys revert`, mutation tools, CLI permission cards.
> Earlier: ✅ **P1 (all)** — headless
> agent runs end-to-end: `genisys run` REPL with streaming render, native + XML
> tool-calling, retries, capability probe, 6 tools, interjections (`/steer` `/queue`).
> See `internal/{model,apiconfig,agent,tools}`, `cmd/genisys`, `.github/workflows/ci.yml`.
> All gates green locally: `make build`, `go test -race ./...`, parser fuzzing,
> 4-OS cross-compile smoke, round-trip comment preservation. The M0 exit gate now
> needs live endpoints only (decision D1).
> Completed tasks are marked ✅ inline in the phase tables below
> (legend: ✅ done · 🟡 in progress · ⬜ not started).

This plan turns SPEC v0.2 (Go, YAML-only config, user-controlled interjection) into an
ordered, dependency-aware work breakdown. Section references (`§x`) point at SPEC.md
unless prefixed with `IMPL §`. Effort estimates assume one experienced Go/TS developer
working part-time; "SP" = subjective story points (1 SP ≈ half a day).

---

## Table of Contents

1. [Prerequisites & Dev Environment](#1-prerequisites--dev-environment)
2. [Repository Layout (buildable skeleton)](#2-repository-layout-buildable-skeleton)
3. [Go Module & Dependencies](#3-go-module--dependencies)
4. [Core Internal Interfaces](#4-core-internal-interfaces)
5. [Phase 0 — Bootstrap (P0)](#phase-0--bootstrap-p0--sp3)
6. [Phase 1 — Model Client & CLI Loop (P1 ⇒ SPEC M0)](#phase-1--model-client--cli-loop-p1--spec-m0--sp13)
7. [Phase 2 — Harness Core (P2 ⇒ SPEC M1)](#phase-2--harness-core-p2--spec-m1--sp16)
8. [Phase 3 — Server API & UI Skeleton (P3 ⇒ SPEC M2)](#phase-3--server-api--ui-skeleton-p3--spec-m2--sp21)
9. [Phase 4 — Documents & Delivery (P4 ⇒ SPEC M3)](#phase-4--documents--delivery-p4--spec-m3--sp13)
10. [Phase 5 — Discovery, Web, Skills, MCP (P5 ⇒ SPEC M4)](#phase-5--discovery-web-skills-mcp-p5--spec-m4--sp14)
11. [Phase 6 — Polish (P6 ⇒ SPEC M5)](#phase-6--polish-p6--spec-m5--sp13)
12. [Cross-Cutting Workstreams](#cross-cutting-workstreams)
13. [Testing Strategy (detailed)](#13-testing-strategy-detailed)
14. [CI/CD Pipeline](#14-cicd-pipeline)
15. [Dependency Graph & Critical Path](#15-dependency-graph--critical-path)
16. [Risk Register](#16-risk-register)
17. [Definition of Done](#17-definition-of-done)
18. [Milestone Schedule (indicative)](#18-milestone-schedule-indicative)
19. [Decisions Needed Before P1](#19-decisions-needed-before-p1)

---

## 1. Prerequisites & Dev Environment

| Item | Requirement | Notes |
|---|---|---|
| Go | ≥ 1.23 | `go.mod` pins `go 1.23`; no CGO (`CGO_ENABLED=0` everywhere) |
| Node | ≥ 20 LTS | only for `ui/` build; not needed for P1–P2 |
| Target endpoint | one remote OpenAI-compatible server | **decision needed, IMPL §19 D1** — dev/test endpoint + model(s) |
| Second endpoint | any second server for the endpoint matrix (llama.cpp or vLLM) | can be added later; P1 AC needs two |
| Optional local tools | `ripgrep` (search fast-path), `libreoffice` (doc previews), `bwrap`/`nsjail` (sandbox Tier 2) | all optional with graceful degradation |
| OS targets | Linux (primary dev), macOS + Windows (CI build-only until P6) | SPEC G12 cross-compile |

Repo bootstrapping commands live in IMPL §5 (P0). The dev endpoint is configured purely
via YAML + env var (SPEC §18):

```yaml
# ~/.genisys/config.yaml (dev example)
endpoints:
  - id: dev
    name: Dev endpoint
    base_url: http://REPLACE_ME/v1
    api_key_env: GENISYS_DEV_KEY
    models:
      - id: REPLACE_ME
        context_window: 131072
        supports: { tools: native, vision: false }
```

---

## 2. Repository Layout (buildable skeleton)

Exactly SPEC §19, extended to file granularity for implementers:

```
Genisys/
├── docs/                            # SPEC.md, IMPLEMENTATION.md, images/
├── go.mod                           # module github.com/<org>/genisys
├── Makefile                         # build, test, lint, ui, embed-check, dist
├── cmd/genisys/
│   └── main.go                      # cobra-less flag dispatch: serve|run|revert|config
├── internal/
│   ├── apiconfig/                   # §18 YAML engine (name avoids clash with api/)
│   │   ├── schema.go                # typed structs + defaults (single source of truth)
│   │   ├── load.go                  # 3-level load + merge (§18.1)
│   │   ├── roundtrip.go             # comment/order-preserving writes (yaml.v3 Node API)
│   │   └── validate.go              # lint + effective-merge dump
│   ├── model/
│   │   ├── client.go                # chat/completions, SSE streaming (§13.1)
│   │   ├── sse.go                   # minimal SSE line parser (no dep)
│   │   ├── retry.go                 # backoff + Retry-After (§13.3)
│   │   ├── profile.go               # capability profiles + probe (§13.2)
│   │   ├── usage.go                 # token accounting feed (§24)
│   │   └── dto.go                   # wire types (OpenAI shapes)
│   ├── agent/
│   │   ├── loop.go                  # §7.1 loop as a state machine
│   │   ├── prompt.go                # §8 template + text/template rendering (go:embed)
│   │   ├── parser/                  # §13.4: native.go, xml.go, repair.go (own pkg: fuzz target)
│   │   ├── interject.go             # §7.4 queue + boundary injection + cancel
│   │   ├── contextmgr/              # §24 aging, compaction, token meter
│   │   ├── subagent.go              # §12
│   │   └── events.go                # §21 event types (single typed source, persisted + streamed)
│   ├── tools/
│   │   ├── registry.go              # Tool interface, schema registry, dispatch (§7.1)
│   │   ├── fileio/                  # read_file_lines, read_file_chars, replace_file, edit_file_tool
│   │   ├── fileops/                 # delete, move, copy, create_folder, list_dir
│   │   ├── search/                  # search_file_line, find_files (doublestar + optional rg)
│   │   ├── exec/                    # shell_command (process groups), view_images
│   │   ├── find/                    # agentic_find (calls agent/subagent)
│   │   ├── web/                     # web_search, web_extract (§16)
│   │   ├── ui/                      # open_file_in_side_editor, open_url_in_app_browser,
│   │   │                            # attach_file, load_file_attachment, get_scratchpad_folder,
│   │   │                            # deliver_file_to_user
│   │   ├── perms/                   # request_readonly_access, request_read_write_access
│   │   ├── py/                      # run_python (calls sandbox/)
│   │   └── mcp/                     # get_mcp_info, get_mcp_tools_info, call_mcp_tool
│   ├── workspace/
│   │   ├── writer.go                # WorkspaceWriter critical section (§7.2/§10)
│   │   ├── grants.go                # path resolution, symlink escape, grant table (§10)
│   │   └── checkpoint/              # go-git shadow repos + ledger (§11)
│   ├── sandbox/
│   │   ├── runner.go                # Tier dispatch + /inputs /outputs contract (§17.1)
│   │   ├── wazero/                  # CPython-WASM guest, WASI, in-memory FS
│   │   ├── proc/                    # bwrap/nsjail/sandbox-exec fallback
│   │   └── embed/                   # go:embed: cpython.wasm, wheels/, paperworks/
│   ├── skills/                      # §14 discovery, frontmatter parse, @-palette feed
│   ├── mcp/                         # §15 client (stdio + http), lazy lifecycle
│   ├── store/
│   │   ├── db.go                    # modernc.org/sqlite, WAL, migrations (go:embed SQL)
│   │   ├── migrations/0001_init.sql # §6.2 schema
│   │   ├── projects.go sessions.go messages.go events.go checkpoints.go permissions.go
│   │   └── transcript.go            # §6.3 jsonl read/append
│   ├── supervisor/                  # session worker registry, concurrency cap, orphan recovery
│   ├── api/
│   │   ├── rest.go                  # chi routes: CRUD projects/sessions/messages/files/settings/checkpoints
│   │   ├── ws.go                    # §21 WS: subscribe frames, replay from events table
│   │   └── static.go                # go:embed ui/dist + SPA fallback
│   └── server/
│       └── server.go                # wiring: store+supervisor+api, graceful shutdown
├── ui/                              # §20 React app (own package.json; vite build → dist)
├── testdata/
│   ├── golden/                      # recorded endpoint transcripts (replay fixtures)
│   ├── corpus/                      # §4.2 acceptance tasks + expected artifacts
│   └── endpoints.yaml               # endpoint-matrix config (CI nightly)
└── scripts/
    ├── gen-golden.go                # records a live session into testdata/golden
    └── fuzz-report.sh
```

Rules:
- `internal/` enforces encapsulation; only `cmd/` composes.
- **No import cycles rule:** `agent` depends on `tools/registry` (interface), never on tool
  implementations; tool packages depend on `agent/events` types only. Wiring happens in
  `cmd/` (constructor injection).
- Every package gets a `doc.go` with a one-paragraph responsibility + SPEC §-refs.

---

## 3. Go Module & Dependencies

Module: `github.com/<org>/genisys` (final org TBD, IMPL §19 D2).

| Dependency | Used for | SPEC |
|---|---|---|
| `gopkg.in/yaml.v3` | all config; Node API for round-trip writes | §18 |
| `modernc.org/sqlite` | pure-Go SQLite driver (no cgo) | §19 |
| `github.com/go-chi/chi/v5` | REST router | §19 |
| `github.com/gorilla/websocket` | WS event stream | §21 |
| `github.com/go-git/go-git/v5` | shadow checkpoint repos | §11 |
| `github.com/tetratelabs/wazero` | embedded CPython-WASM sandbox | §17.1 |
| `github.com/pkoukk/tiktoken-go` | token estimation | §24 |
| `github.com/bmatcuk/doublestar/v4` | glob matching (`find_files`) | §9 |
| `github.com/sabhiram/go-gitignore` | ignore-rule parsing | §9 |
| `github.com/mark3labs/mcp-go` (client only) | MCP stdio/http client | §15 |
| `go.starlark.net` | *(optional, P6)* `genisys_tool` scripting | §9.8 |
| stdlib only | SSE parsing, HTTP client, `text/template`, `os/exec`, `os/signal` | — |

Deliberately **not** used: cgo SQLite (`mattn`), ORMs (sqlx at most, raw SQL preferred),
viper (YAML engine is hand-rolled on yaml.v3), protobuf. `go.mod` kept < 25 direct deps;
`make deps-audit` in CI.

---

## 4. Core Internal Interfaces

The seams that let phases be built and tested independently. These are normative for P1–P3.

```go
// ---- tools/registry.go ----------------------------------------------------
type Tool interface {
    Name() string
    Description() string             // model-visible, SPEC §9
    Schema() json.RawMessage         // JSON Schema draft-07
    // Execute must honor ctx (interrupt = ctx cancel, SPEC §7.4) and return
    // an already-truncated result (SPEC §7.3 shaping happens here or in loop).
    Execute(ctx context.Context, call Call, w *World) (Result, error)
}
type World struct {                  // everything a tool may touch, permission-checked
    WS   *workspace.Writer           // grants + checkpoint + diff, §10/§11
    Log  EventSink                   // typed events, §21
    Self func(Call) Tool             // registry lookup (subagents, mcp)
}

// ---- agent/events.go (single typed event source; persisted AND streamed) --
type Event interface{ Type() string; Payload() json.RawMessage }
// concrete: StepStarted, TextDelta, ToolStarted, ToolOutput, ToolFinished,
// FileChanged, PermissionRequested, InterjectionQueued/Promoted/Withdrawn/
// Delivered, InterruptStarted, Usage, StepFinished, SessionStatus  (SPEC §21)

// ---- model/client.go ------------------------------------------------------
type Client interface {
    // Stream returns a channel of deltas; ctx cancel aborts the SSE read
    // (used by interrupt, SPEC §7.4).
    Stream(ctx context.Context, req ChatRequest) (*StreamHandle, error)
    ListModels(ctx context.Context) ([]string, error)
}
type StreamHandle struct {
    Events <-chan StreamEvent        // text_delta | tool_call_delta | usage | done
    Err    error                    // post-close terminal error
}

// ---- agent/interject.go (SPEC §7.4) ---------------------------------------
type InterjectionMode string
const ( ModeQueue InterjectionMode = "queue"; ModeInterrupt = "interrupt" )
type Interjection struct { ID, Text string; Mode InterjectionMode; … }
type Interjector struct{ /* queue, mu, notify chan */ }
func (q *Interjector) Push(Interjection)            // from WS/CLI while running
func (q *Interjector) DrainBoundary() []Interjection // called at tool_batch/step_end
func (q *Interjector) Promote(id string) bool       // queue → interrupt

// ---- workspace/writer.go (non-preemptible critical section, SPEC §7.4) ----
type Writer interface {
    // Mutate: grant-check → checkpoint → apply → diff → events.
    // Holds the mutation lock; ctx cancel is deferred until it returns.
    Mutate(ctx context.Context, stepID string, desc string, fn Txn) (Diff, error)
    Read(path string) ([]byte, error)                 // grant-checked
}
type Txn func(tx Tx) (Diff, error)                    // tx: typed file ops

// ---- store/transcript.go (resume source of truth, SPEC §6.3) --------------
type Transcript interface {
    Append(msg Message, env EventEnvelope) error      // fsync-per-line
    Replay() iter.Seq[Entry]                          // incl. step/env events
}
```

Design notes:
- **Events are the spine:** the loop never calls the UI; it emits typed events that are
  simultaneously (a) persisted to the `events` table and (b) fanned out to WS subscribers.
  Replay from `last_event_id` gives gap-free reconnect (SPEC §21) for free.
- **Interrupt = `context.CancelFunc` per step** + process-group kill in `tools/exec` +
  `Interjector.Promote`. The only non-preemptible region is `Writer.Mutate` (SPEC §7.4).
- **Parser is its own package** (`agent/parser`) with zero deps beyond stdlib — it is the
  fuzzing target (SPEC §25 test strategy) and must be usable by the CLI and server alike.

---

## 5. Phase 0 — Bootstrap (P0) — 3 SP · ✅ complete (2026-11-24)

*Goal: repo compiles, CI green, config engine real. Everything after builds on this.*

| Task | Description | SPEC | DoD | SP |
|---|---|---|---|---|
| ✅ P0-1 | `go mod init`, layout per IMPL §2, `Makefile` (`build/test/lint/ui/dist`), golangci-lint config, `.editorconfig` | §19 | `make build` produces `genisys` binary | 1 |
| ✅ P0-2 | `internal/apiconfig`: typed schema for `config.yaml`/`project.yaml`/`session.yaml`, 3-level merge, validation errors with file:line | §18 | `genisys config validate` + `config show` work; round-trip test preserves comments & key order | 2 |
| ✅ P0-3 | CI skeleton (IMPL §14): build matrix (linux/amd64, linux/arm64, darwin/arm64, windows/amd64), lint, unit tests | §19 | green pipeline on empty test suite | 0.5 |

**Exit gate:** ✅ met (2026-11-24) — `genisys config validate` against a hand-written dev `config.yaml` reports
the resolved endpoint + model; editing YAML by hand and re-running shows updated merge.

---

## 6. Phase 1 — Model Client & CLI Loop (P1 ⇒ SPEC M0) — 13 SP · 🟡 in progress (1/9)

*Goal: SPEC M0 exactly — headless agent that fixes a bug in a repo against two endpoints.*

| Task | Description | SPEC | DoD | SP |
|---|---|---|---|---|
| ✅ P1-1 | `internal/model`: DTOs, SSE parser, streaming `Stream()`, usage capture | §13.1 | golden-file SSE replay tests pass | 2 |
| ✅ P1-2 | `model/retry.go`: `RetryPolicy` backoff loop, `Retry-After` (delta-seconds + HTTP-date), mid-stream-drop policy (complete tool calls accepted, prose-only retried), think-tag stripping | §13.3 | httptest: 429+Retry-After, 5×500 exhaustion, auth no-retry, drop-with-tool-calls accepted, drop-prose retried | 1 |
| ✅ P1-3 | `model/profile.go`: capability probe (independent max_tokens / tools / stream_options / think-tag tests), quirks applied per request (`WithQuirks`), profile YAML already in schema | §13.2 | probe classifies tools native/xml/none + quirks on httptest fakes; live-endpoint confirmation pending D1 | 1 |
| ✅ P1-4 | `internal/agent/parser`: native accumulator (`FromNative`) + XML fallback (fences, single quotes, trailing commas, missing close tags, `antml:` namespaces) + `RepairJSON` (smart quotes, Python literals, unquoted keys, unbalanced braces) | §13.4 | table-driven suite + `FuzzParse` (12M+ execs, no panics) | 3 |
| ✅ P1-5 | `tools/registry` (OpenAI-shaped defs, §7.3 result shaping/truncation, unknown-tool recovery hint) + 6 tools incl. `shell_command` with process-group kill (unix/windows) | §9.1/§9.3 | unit tests on `testdata/fixturerepo` incl. timeout + kill-group | 2 |
| ✅ P1-6 | `agent/loop.go` v1: cancellable step machine (model call + tool batch share one step ctx), parallel tool batch, step budget → forced wrap-up turn, identical-batch loop detection nudge | §7.1–7.3 | httptest mock-endpoint tests: native + XML flows, budget, nudge; golden-transcript replay vs live endpoint deferred to D1 | 2 |
| ✅ P1-7 | `agent/prompt.go` + `templates/system.tmpl` (`go:embed`): identity, coding/git, communication, workspace locations, tools mode native/xml/none, environment block; re-rendered per request | §8 | structure snapshot test + tiktoken budget test: **1 502 tokens** ≤ 6k with 6 tool schemas | 1 |
| ✅ P1-8 | `genisys run` REPL: config-driven endpoint/model/quirks, event-spine terminal render (steps, tool chips, usage), Ctrl-C = stop current task / quit when idle, graceful drain on `/quit` | §7.4 | E2E smoke vs scripted mock servers (native + XML) passes | 1 |
| ✅ P1-9 | **Interjection v1 (CLI):** `agent/interject.go` queue + interrupt signal; `/queue` delivered at tool_batch boundary with `[user interjection…]` prefix; `/steer` cancels step ctx → process-group kill + `[interrupted…]` results; `/pending` + withdraw-ready | §7.4 | AC met in `TestInterruptKillsToolAndHonorsNewInstruction`: sleep killed <3 s, pre-sleep effect kept, post-sleep effect absent, new instruction honored first | 1 |

**Exit gate (SPEC M0 AC):** scripted run on fixture repo against **two** endpoints
(configurable via `testdata/endpoints.yaml`) completes bug-fix + test-run; parser survives
a deliberately fenced-JSON model (recorded golden). `testdata/golden/` populated via
`scripts/gen-golden.go`.

---

## 7. Phase 2 — Harness Core (P2 ⇒ SPEC M1) — 16 SP

*Goal: SPEC M1 — safe, resumable, checkpointed mutations with grants and transcripts.*

| Task | Description | SPEC | DoD | SP |
|---|---|---|---|---|
| ✅ P2-1 | `internal/store`: modernc sqlite (pure Go), WAL/busy_timeout/FK, embedded migrations w/ up+down, full §6.2 schema incl. `events` ledger; ULID-ish sortable ids | §6.2 | migrate up/down/idempotency test; 16×20 concurrent-writer test | 2 |
| ✅ P2-2 | `store/transcript.go`: jsonl append w/ fsync per line + tolerant replay; `agent.Resume` rebuilds messages (system prompt re-rendered, never replayed); CLI `--resume [id\|latest]` | §6.3 | simulated kill-9 after tool result → resume continues w/ intact context (test + E2E: "resumed 10 persisted messages") | 2 |
| ✅ P2-3 | `workspace/grants.go`: canonical resolution (deepest-existing-ancestor realpath), cwd/scratch policy, session grant table persisted via permissions table + reloaded on resume | §10 | table tests: `../` + relative-`../` + symlink-escape + ro→rw escalation + new-file-under-granted-dir | 2 |
| ✅ P2-4 | `workspace/writer.go`: single mutation path — ctx checked before entry, mutex critical section non-preemptible, read-before-write (sha256+mtime), unified-diff capture, `file_changed`/`checkpoint_created` events, Snapshot/Restore API | §7.2/§7.4 | `TestWriterCriticalSectionNotPreempted`: cancel mid-write → file complete, post-cancel mutate denied | 2 |
| ✅ P2-5 | `workspace/checkpoint.go`: go-git shadow repo (git-dir outside worktree; storer-init bypasses go-git's .git-pointer write), pre-mutation commits (AllowEmptyCommits), ledger rows, `genisys revert --step/--list`, GC w/ loose-object prune, FileHistory for the Files tab | §11 | revert restores exactly (created files removed); user `.git` byte-identical after checkpoint+revert; GC keeps newest N | 3 |
| ✅ P2-6 | `tools/mutate`: edit_file_tool (exact-once matches, CRLF→LF normalize, all-or-nothing w/ line-anchored diagnostics), replace_file, delete, move, copy, create_folder — all through `Writer.Mutate` | §9.2 | `TestPropertyEditSequenceReplay`: 12 random edits, every snapshot restores byte-exact prior state | 2 |
| ✅ P2-7 | `workspace/broker.go` + `tools/perms`: request_readonly/read_write tools, pending row in permissions table, loop suspends (CLI prompt = WS-card stand-in), 10-min timeout → "user did not respond", interrupt → "interrupted", grants persisted + reloaded on resume; shell deny-list (sudo, rm -rf /, pipe-to-sh) raises a card | §9.7/§10 | broker tests incl. interrupt-during-pending; E2E: read outside cwd denied → request → `y` → read succeeds | 1 |
| ✅ P2-8 | per-session scratchpad `~/.genisys/scratchpad/<session-id>/` (rw always, outside workspace), `get_scratchpad_folder` tool | §9.6 | scratchpad is a separate granted root; E2E: nothing lands in cwd | 0.5 |
| ✅ P2-9 | every message persists the moment it lands (transcript fsync + messages table, per-call in tool batches); boot: orphan sessions → error, pending permissions → denied; torn-tail transcript line skipped on replay; Session mutex (UI reads while loop writes) | §7.2/§23 | torn-tail resume test + kill-after-tool-result resume test; race suite green | 1.5 |

**Exit gate (SPEC M1 AC):** every mutation reversible by `genisys revert <step>`; kill -9
mid-session resumes cleanly; grants enforced even when the model hallucinates absolute
paths.

---

## 8. Phase 3 — Server API & UI Skeleton (P3 ⇒ SPEC M2) — 21 SP

*Goal: SPEC M2 — the three-pane app, live events, interjection UI. Biggest phase; split
into backend (P3a) and frontend (P3b) tracks that proceed in parallel after P3-2.*

### P3a — Server (9 SP)

| Task | Description | SPEC | DoD | SP |
|---|---|---|---|---|
| P3-1 ✅ | `internal/api/rest.go`: CRUD projects/sessions/messages/files/settings/checkpoints; project create flow (work + code, folder pick via client) | §5.6, §6 | REST integration tests | 2 |
| P3-2 ✅ | `internal/api/ws.go`: subscribe frames, typed event fan-out, `last_event_id` replay from ledger | §21 | reconnect-mid-step test replays gap-free | 2 |
| P3-3 ✅ | `internal/supervisor`: goroutine worker registry, cancellable contexts, concurrency cap (4) + queue, orphan recovery | §19 | 5 concurrent sessions test; cap respected | 1.5 |
| P3-4 ✅ | Interjection over the wire: `send_message {mode}`, promote/withdraw/reorder frames, queue state events | §7.4/§21 | e2e-ws tests for both modes incl. promote | 1.5 |
| P3-5 ✅ | Permission + UI tools on the server: `permission_requested` round-trip, `open_file_in_side_editor`, `open_url_in_app_browser`, `attach_file`, `deliver_file_to_user` (events + file serving routes) | §9.6/§9.7 | card flows tested headlessly via ws client | 1 |
| P3-6 ✅ | `api/static.go`: go:embed `ui/dist`, SPA fallback, localhost-only bind + bearer token (single-user auth) | §19/§22 | token required; 0.0.0.0 opt-in warns | 0.5 |
| P3-7 ✅ | `genisys serve`: single command starts server + prints URL + opens browser | G12 | one-command dev & prod experience | 0.5 |

### P3b — Frontend (12 SP)

| Task | Description | SPEC | DoD | SP |
|---|---|---|---|---|
| P3-8 ✅ | `ui/` scaffold: Vite+React+TS+Tailwind+shadcn, Zustand store reducing the event stream, WS client w/ auto-reconnect + `last_event_id` | §20/§21 | event reducer unit tests (vitest) | 2 |
| P3-9 ✅ | Three-pane shell: ProjectRail, SessionTabs + resizable split panes, right-panel tabstrip, collapsible panes persisted | §5.1/§5.2 | layout matches `docs/images/dark-home-demo-macos.webp` structurally | 2 |
| P3-10 ✅ | MessageStream: user bubbles, streaming assistant, `Worked for …` StepTrace (live tail, expandable chips), action row (copy/fork/retry) | §5.3 | virtualized list survives 10k-message replay | 2 |
| P3-11 ✅ | DiffCard from `file_changed` events (+N −M, expand → Editor diff mode, `↺ Revert step`) | §5.3/§11 | counts match server diff exactly | 1.5 |
| P3-12 ✅ | Composer: attachments `+`, model chip + picker, `@`-palette (files; skills stub), context meter, hint row | §5.3 | palette keyboard nav | 2 |
| P3-13 ✅ | **Interjection UI:** split send button while running (+Shift invert per config), pending-queue chip with promote/reorder/withdraw, interrupt confirmation card | §5.3/§7.4 | Playwright: send-while-running both modes; promote works | 1.5 |
| P3-14 ✅ | FilesTree (git badges, context menu incl. rollback) + EditorPane (Monaco, diff mode via diff2html) | §5.4 | tree updates live from `file_changed` | 1.5 |

**Exit gate (SPEC M2 AC):** acceptance corpus rows 1 & 3 pass in the UI; two concurrent
sessions in tabs; tab close/reopen replays gap-free; mid-run send offers interrupt vs.
queue, both behave per §7.4, queued chip promotable, interrupt card accurate.
*Backend half of the exit gate is covered by `internal/api` e2e tests (concurrency cap,
gap-free WS replay, both interjection modes, permission round-trip, revert); the
browser-level Playwright job (`e2e-mock`) is the remaining CI wiring task.*

---

## 9. Phase 4 — Documents & Delivery (P4 ⇒ SPEC M3) — 13 SP

| Task | Description | SPEC | DoD | SP |
|---|---|---|---|---|
| P4-1 | `sandbox/wazero`: CPython-WASM guest build pipeline (pinned version, committed `cpython.wasm`), WASI-lite + in-memory `/inputs` `/outputs`, package wheel mounting (Pillow, pypdf, reportlab, docx, pptx, openpyxl) | §17.1 | `run_python` creates a 1-page pdf + 3-slide pptx in CI (no system python) | 4 |
| P4-2 | `sandbox/proc`: Tier-2 runner (bwrap/nsjail/sandbox-exec/Docker autodetect), same contract, capability report | §17.1 | matrix test on linux+macos runners | 2 |
| P4-3 | `paperworks` library (Python, embedded via go:embed): `ppt_builder.Deck`, `docx_redline`, xlsx helpers; prompt hints wired into Researcher persona | §17.1 | deck generated via paperworks passes python-pptx re-open check | 2 |
| P4-4 | `tools/py`: `run_python` with `stage_files` validation (`/inputs/` prefix), timeout, output copy-back to scratchpad, event streaming (`tool_output`) | §9.3 | interrupt mid-run kills guest cleanly (wazero close / proc group kill) | 1.5 |
| P4-5 | Delivery: `deliver_file_to_user` end-to-end (scratchpad → workspace, overwrite rules), `load_file_attachment`, attachment upload REST | §9.6 | corpus row 1 (memo from attached proposals) | 1 |
| P4-6 | Previews: md/pdf/images client-side; pptx/docx/xlsx → PNG/HTML via sandbox+libreoffice route, graceful fallback | §17.2 | deck opens in right panel (or documented fallback) | 2 |
| P4-7 | Researcher persona prompt block + `documents-presentations-spreadsheets` built-in skill | §8/§14 | corpus row 2: valid 6-slide deck + assumptions section | 0.5 |

**Exit gate (SPEC M3 AC):** acceptance corpus row 2 produces a valid 6-slide deck opened
in preview, assumptions section present; zero system-Python dependency on the default path.

---

## 10. Phase 5 — Discovery, Web, Skills, MCP (P5 ⇒ SPEC M4) — 14 SP

| Task | Description | SPEC | DoD | SP |
|---|---|---|---|---|
| P5-1 | `agent/subagent.go`: locator system prompt, 4-tool restriction, budgets (12 steps/90 s/4 KB), parallel queries, nested trace events | §12 | `agentic_find` locates code in fixture monorepo; one failing query doesn't sink others | 3 |
| P5-2 | `tools/find`: `agentic_find` wiring (≤4 queries, cwd resolution) | §9.4 | corpus: "find the HTTP client" → correct files cited | 1 |
| P5-3 | `tools/web`: `web_search` (provider iface: searxng/tavily/brave/none, 24 h cache), `web_extract` (readability→md, 8 KB/URL, per-URL failure isolation, `<web_content trust="untrusted">` wrapping) | §9.5/§16/§22 | httptest-backed provider tests; toggle hides tool from schema | 2.5 |
| P5-4 | `internal/skills`: SKILL.md frontmatter parse, discovery roots, `<available-skills>` rendering, `@`-palette feed, explicit-invocation instruction injection | §14/§8 | a Claude-Code skill from `~/.claude/skills` runs unmodified (test fixture) | 2.5 |
| P5-5 | `skill-management` built-in skill incl. install-from-URL (fetch → validate → place) | §14 | install a skill from a GitHub raw URL end-to-end | 1.5 |
| P5-6 | `internal/mcp`: stdio+http clients, lazy start/idle timeout, progressive disclosure tools (`get_mcp_info`/`get_mcp_tools_info`/`call_mcp_tool`), permission gating | §15/§9.8 | echo-server integration test; schemas never in base prompt (snapshot) | 2.5 |
| P5-7 | In-app browser pane (iframe localhost + allow-list) + `open_url_in_app_browser` (focus-or-open semantics) | §5.4/§9.6 | corpus row 4: live preview of dev server | 1 |

**Exit gate (SPEC M4 AC):** all §4.2 corpus rows pass; skill import from Claude Code works;
MCP echo callable; web toggle fully disables web tools.

---

## 11. Phase 6 — Polish (P6 ⇒ SPEC M5) — 13 SP

| Task | Description | SPEC | DoD | SP |
|---|---|---|---|---|
| P6-1 | Fork sessions (branch transcript), pin/rename/archive/delete flows, background completion badges | §5.2/§5.3 | fork mid-run branches cleanly | 2 |
| P6-2 | Context management: result aging, compaction at 80% (structured summary, "Context compacted" marker), overflow auto-compact-retry | §24/§23 | long-session soak: no overflow hard-fail; compaction marker visible | 3 |
| P6-3 | Checkpoint browser UI (file history timeline, restore), user-edit-overwrite warning | §11/§5.4 | restore any file to any step | 2 |
| P6-4 | Settings UI complete: Endpoints CRUD + Test connection, Skills manager, Browser allow-list, theme light/dark/system (both palettes per `docs/images/`) | §5.5/§18 | every settings screen round-trips YAML (comment-preservation e2e) | 2.5 |
| P6-5 | Error cards everywhere: Retry / Switch model / Edit last message; parse-failure nudges visible | §23 | endpoint kill/restore mid-session recovers | 1.5 |
| P6-6 | `genisys_tool` + `introspection`/`file-history` built-in skills | §9.8/§14 | agent can summarize its own session | 1 |
| P6-7 | Distribution: **Linux AppImage (SPEC §19.1/G13, primary artifact)** via `make dist` (appimagetool: binary + .desktop + icon) + 4 OS/arch tarballs, version stamping, `genisys version`; README install docs. macOS/Windows releases planned for the future — cross-compile stays green, packaging deferred | G12, G13 | AppImage runs on clean Ubuntu LTS VM (`chmod +x`, no other installs); CI artifacts downloadable; cold start on clean Linux VM < 2 min incl. config | 1 |

**Exit gate (SPEC M5 AC):** all corpus rows pass on a mid-size open model (32B class)
with zero harness errors.

---

## Cross-Cutting Workstreams

Not phases — continuous tracks with owners and standing tasks:

- **XX-1 Prompt engineering (every phase):** prompt templates live in
  `internal/agent/templates/` with golden snapshot tests; every change re-runs the corpus
  replay suite. Track token budget (≤ 6k, SPEC §8) in CI.
- **XX-2 Open-model robustness (P1→P6):** maintain a "model zoo" fixture set — recorded
  transcripts from ≥3 model families (native-tool, XML-tool, sloppy-JSON). Fuzz corpus for
  `agent/parser` grows continuously; nightly endpoint matrix (IMPL §14) against real servers.
- **XX-3 Security review (P2, P4, P6 gates):** path-grant table tests, shell deny-list
  updates, secret redaction checks, prompt-injection wrapping checks (§22) reviewed at
  each gate, not only at the end.
- **XX-4 Docs (every phase):** `docs/` user guide grows per feature; ADRs in
  `docs/adr/` for any deviation from SPEC (numbered, dated — deviations must cite the
  SPEC § they change).

---

## 13. Testing Strategy (detailed)

| Layer | Tooling | Scope | Runs |
|---|---|---|---|
| Unit | `go test` + `testing/fstest`/`httptest` | every `internal/` pkg; config merge; grants; parser tables | every push |
| Golden transcript replay | `scripts/gen-golden.go` fixtures | loop determinism: recorded endpoint sessions replay to identical transcripts + diffs + events | every push |
| Fuzz | `go test -fuzz` (parser pkg) | malformed tool syntax (fences, quotes, truncation) | 60 s PR / 10 min nightly |
| Property | `go-fuzz`-lite random ops | edit sequences ↔ checkpoint replay equivalence (P2-6) | nightly |
| Crash matrix | kill-at-state harness | SIGKILL at each loop state → resume correctness | nightly |
| Endpoint matrix | `testdata/endpoints.yaml` | ≥2 real servers (llama.cpp, vLLM or user's remote) incl. one deliberately XML-mode model | nightly |
| Frontend unit | vitest | event reducers, WS reconnect | every push |
| E2E | Playwright + scripted mock endpoint | corpus rows, interjection UI, permission cards, reconnect | every push (mock) / nightly (real) |
| Soak | long-session script (200+ steps) | compaction, aging, GC, memory profile | nightly |

Coverage bar: `internal/agent`, `internal/model`, `internal/workspace` ≥ 85% statement;
everything else ≥ 65%. Race detector on all Go CI jobs (`-race`).

---

## 14. CI/CD Pipeline

GitHub Actions (or equivalent), all jobs on `pull_request` + `main`:

```
lint        → gofmt/goimports, golangci-lint, eslint+prettier (ui/), deps-audit
build       → matrix: linux/amd64, linux/arm64, darwin/arm64, windows/amd64 (CGO off)
test-go     → go test -race ./... + golden replays
test-ui     → vitest + vite build + embed check (go:embed dist non-empty)
fuzz-brief  → 60 s fuzz on agent/parser
e2e-mock    → Playwright against genisys serve + mock endpoint
nightly     → fuzz-10m, property, crash-matrix, endpoint-matrix (real servers,
              self-hosted runner), soak
release     → tag → make dist (AppImage [primary, G13] + 4 tarballs + checksums) → GitHub Release
```

Mock endpoint: small Go test server (`cmd/mockendpoint`, testdata-driven) that replays
golden transcripts over real SSE — lets E2E exercise tool-calling without any external
service.

---

## 15. Dependency Graph & Critical Path

```
P0 ──► P1 ──► P2 ──► P3a ──► P3b ──► P6
              │       └── P3-2 (WS) gates all P3b
              └── P4-1 (sandbox) can start during P3b (independent)
P2 ──► P5-1/P5-2 (subagents need grants+transcript)
P1 ──► P5-3 (web tools need loop shaping)
P2 ──► P5-4/P5-6 (skills need prompt; mcp needs registry)
```

**Critical path:** P0 → P1-4 (parser) → P1-6 (loop) → P2-4/P2-5 (writer/checkpoints) →
P3-2 (WS) → P3-10/13 (chat + interjection UI) → P6-2 (context mgmt).

De-risking order inside P1: parser (P1-4) is built **before** the loop because it is the
highest-uncertainty component for open models; it is also the most independently testable.

Parallelization options:
- P4-1 (wazero sandbox) and P3b (frontend) are disjoint — run concurrently if two tracks.
- XX-2 model-zoo recording can start as soon as P1-8 exists.

---

## 16. Risk Register

| # | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| R1 | CPython-WASM (wazero) can't run python-pptx/reportlab (C-ext or syscall issues) | Medium | High | P4-1 spike **first week of P4**; pure-python wheels preferred (pptx/openpyxl are pure; reportlab has C ext → ship Tier-2 fallback + prompt hint); decision ADR either way |
| R2 | Remote endpoint flakiness / non-standard OpenAI quirks | High | Medium | profile+quirks system (P1-3), retry taxonomy, mock endpoint decouples CI, endpoint matrix catches regressions |
| R3 | Open model ignores `[user interjection…]` prefix | Medium | Medium | prefix is a real user-role message (models weight user turns); corpus test with 3 model families; prompt rule in §8; worst case boundary=step_end still honored |
| R4 | go-git perf on large worktrees (checkpoint per mutation) | Medium | Medium | sparse staging (only touched paths), benchmark in P2-5, fall back to content-hash snapshot store if needed (ADR) |
| R5 | WS replay ledger growth (events table) | Low | Low | per-session compaction of events older than last checkpoint |
| R6 | Scope creep on UI (Bionic parity) | High | Medium | corpus-driven acceptance only; anything not in §4.2/§5 is explicitly post-M5 |
| R7 | Windows shell semantics (`bash -lc` absent) | Medium | Low | P6-7 windows dist gate; PowerShell runner behind same process-group abstraction; documented limitation until then |

---

## 17. Definition of Done (per task)

A task is done only when all are true:

1. Code merged with tests per IMPL §13 layer for its scope; `-race` clean.
2. SPEC §-refs present in code comments (`doc.go` + exported symbols).
3. Events it emits are typed in `agent/events.go` and persisted (no ad-hoc JSON).
4. If user-visible: works in **both themes**, keyboard-accessible, and covered by one
   Playwright or golden test.
5. Config it introduces exists in YAML schema (`apiconfig`) + docs + settings screen
   (or explicit "CLI-only" ADR).
6. Prompt-affecting changes re-run corpus replays (XX-1).
7. No new direct Go dependency without an ADR.

---

## 18. Milestone Schedule (indicative)

Assumes ~1 dev, part-time; calendar = weeks from P0 start; ranges = optimistic…pessimistic.

| Milestone | Phases | SP | Calendar | Gate |
|---|---|---|---|---|
| **M0** headless agent | P0+P1 | 16 | W1–W4 | P0+P1 code-complete ✅ (16/16 SP) — exit gate needs live endpoints (D1): 2-endpoint bug-fix run + model-zoo goldens |
| **M1** safe harness | P2 | 16 | W5–W8 | revert + kill-9 resume |
| **M2** usable app | P3 | 21 | W9–W14 | corpus 1 & 3 in UI + interjection UX |
| **M3** documents | P4 | 13 | W13–W17 (overlaps P3b) | corpus 2 |
| **M4** full toolset | P5 | 14 | W17–W20 | corpus 4 + skills + MCP |
| **M5** polished v1.0 | P6 | 13 | W21–W24 | full corpus on 32B-class open model |

Total ≈ 93 SP ≈ **24 weeks** single-dev; ≈ 14–16 weeks with the P3b/P4-1 parallelization.

---

## 19. Decisions Needed Before P1

| # | Decision | Default if no answer | Needed by |
|---|---|---|---|
| D1 | **Dev/test endpoint**: base URL, key env var, primary model + one deliberately weak/XML model | any public OpenAI-compatible endpoint + one small local model | **P1-1 (W1)** |
| D2 | GitHub org / module path for `go.mod` | `github.com/larsn/genisys` | P0-1 |
| D3 | Which 32B-class open model is the M5 acceptance model (drives model-zoo recording) | Qwen3-Coder-32B class via D1 endpoint | P1-9 |
| D4 | CI runner availability for nightly endpoint matrix (self-hosted w/ GPU vs. external API endpoint) | external endpoint only | XX-2 |
| D5 | Reportlab (C-ext) vs. pure-python PDF stack if R1 spike fails (weasyprint-out) | drop to Tier-2 sandbox for PDFs only | P4-1 |

---

*End of IMPLEMENTATION.md v0.1.*
