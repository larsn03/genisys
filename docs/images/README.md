# Reference Screenshots — LM Studio Bionic

Visual ground truth for the Genisys recreation (see `../SPEC.md`, §5 User Interface Specification).
All images are screenshots of the official LM Studio Bionic app, downloaded from lmstudio.ai
(blog posts, docs marketing assets) on 2026-11-24. Naming: `{theme}-{feature}.{ext}`.

## Dark mode

| File | Shows | Source |
|---|---|---|
| `dark-home-demo-macos.webp` | Full three-pane UI in dark mode: Projects sidebar (Project management / Paris trip / Build website), chat with "Worked for 15s" trace + "Edited files" diff card (+18 −0 / +7 −2), composer "Ask Bionic to do something" with +/cloud/mic/model buttons, hint row "Build website · main", and in-app browser tab live-previewing `localhost:3000` | lmstudio.ai homepage demo (dark variant, macOS) |
| `dark-home-demo-windows.webp` | Same scene with Windows window chrome | lmstudio.ai homepage demo (dark variant, Windows) |
| `dark-home-demo-macos-narrow.webp` | Narrow-viewport variant of the dark demo | lmstudio.ai homepage demo |
| `dark-mobile-composite.webp` | Mobile composite, dark | lmstudio.ai homepage demo (mobile) |

**Dark-mode palette observed:** near-black panels (~`#101014`) with subtle elevation steps,
white text, same blue accent for the active session, diff colors `+` green / `−` red unchanged,
user bubble dark gray, sidebar labels muted gray, wallpaper-backed window on macOS.

## Light mode — core UI

| File | Shows | Source |
|---|---|---|
| `light-home-demo-windows.jpg` | The same portfolio-site scene in light mode (canonical marketing shot) | lmstudio.ai homepage demo (light, Windows) |
| `light-coding-project.png` | **Code project**: git repo (git/master · Bionic Coder), session list under "LM Studio Bionic" project, markdown answer with code blocks + Copy buttons + `file.c:NNN` citations, composer with model chip "GLM 5.2 High", status bar "Context: 9.3K", right panel = full file tree with git branch footer | docs (`/docs/bionic/agent/code-project`) |
| `light-work-project-pptx.png` | **Work project**: 5-slide Paris itinerary pptx generated; deck outline + "Key assumptions" in chat; right panel = native PowerPoint preview with slide thumbnails; status bar "My First Project · Bionic Researcher · Context: 24.3K" | docs (`/docs/bionic/agent/work-project`) |
| `light-model-picker.png` | Model picker popover: Cloud / Local / Remote (LM Link) model tabs | docs (`/docs/bionic/models`) |
| `light-launch-cover.png` | Launch announcement cover art | blog: introducing-lm-studio-bionic |
| `light-agent-remote-control.png` | Agent remote-control toggle UI | blog: agent-remote-control |

## Light mode — features

| File | Shows | Source |
|---|---|---|
| `light-blog-local-models.png` | In-app local model browser/downloader (Bionic-specific, dropped in Genisys) | blog: introducing-lm-studio-bionic |
| `light-blog-voice-transcription.png` | Voxtral voice dictation UI (deferred in Genisys v1) | blog: introducing-lm-studio-bionic |
| `light-introspection-session-reference.png` | `@`-style session reference: user asks to "See what we discussed in [Explain local inference]" — cross-session linking | blog: introducing-introspection |
| `light-introspection-session-summary.png` | Introspection: summarized other session in 1 sentence, "Worked for 5s" trace | blog: introducing-introspection |
| `light-introspection-cross-session-permission.png` | Cross-session read permission card | blog: introducing-introspection |
| `light-introspection-compaction-diagram.png` | Context compaction diagram (pre-compaction context → compaction → post-compaction + introspection pointer) — informs SPEC §24 | blog: introducing-introspection |
| `light-work-create-project.webp` | Project creation dialog | blog: kimi-k3-bionic-documents-slides |
| `light-work-pptx-response.webp` | Chat response after generating a PowerPoint (file card + summary) | blog: kimi-k3-bionic-documents-slides |
| `light-work-pptx-preview.webp` | Generated slide rendered in the right-panel preview | blog: kimi-k3-bionic-documents-slides |
| `light-work-document-images.webp` | Adding pictures to a generated document | blog: kimi-k3-bionic-documents-slides |
| `light-skills-composer.png` | "Use skill" option in the chat composer | blog: skills |
| `light-skills-at-palette.png` | `@` reference palette with skills | blog: skills |
| `light-skills-create.png` | Asking Bionic to create a skill from completed work | blog: skills |
| `light-skills-install.png` | Installing a skill from a URL | blog: skills |

## Key UI takeaways encoded in SPEC.md §5

- Three-pane layout, collapsible right panel; tabs for sessions AND for right-panel content.
- Chat: "Worked for {duration} ▸" collapsible step trace; diff cards with per-file `+N −M`;
  copy/fork/retry action row; composer with `+`, mic, model chip; hint row
  `dir · branch · persona` + `Context: N.NK`.
- Personas rendered as colored labels: **Bionic Coder** (orange) / **Bionic Researcher** (orange)
  → Genisys Coder / Genisys Researcher.
- Light AND dark themes are first-class; dark mode keeps identical layout, only surfaces change.
