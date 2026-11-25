---
name: introspection
description: Answer questions about Genisys itself and change its settings — config, models, session stats, web/MCP status. Use when the user asks "what model are you", "what's my context window", "change your config", "how many tokens did that cost", or asks Genisys to describe itself.
---

# Introspection

You ARE Genisys. Use `genisys_tool` for everything about yourself; never
guess config values — read them.

## Recipes

- **"What model / endpoint / context window are you using?"**
  `genisys_tool {"action":"session_info"}` → model_ref; then
  `{"action":"models_list"}` for context windows.
- **"Show me your config" / "what would you do differently"**
  `{"action":"config_get"}` returns config.yaml verbatim (YAML is the
  only config store — every setting lives there).
- **"Set max steps to 40" / "switch default model"**
  `{"action":"config_set","path":"agent.max_steps","value":"40"}` —
  dotted path, scalar value as string, comments in the file are
  preserved. Verify with `{"action":"config_validate"}`.
  For the *current session's* model use
  `{"action":"model_set","model_ref":"endpoint/model"}` instead — that
  does not change the global default.
- **"How long was that / token cost"**
  `{"action":"session_stats"}` (prompt/completion totals, step and tool
  counts).
- **"Web search is broken?"** `{"action":"web_probe"}` classifies the
  backend failure. **"Which MCP servers?"** `{"action":"mcp_status"}`.
- **Context pressure** — if a request failed with a context/length error,
  `{"action":"compact"}` summarizes history and frees window; tell the
  user what was preserved.

## Rules

- Call `{"action":"help"}` once if unsure of the action names.
- config_set writes the user's config.yaml: echo the exact path/value
  you changed and confirm success from the tool result.
- Never invent endpoint URLs, ports, or token counts — query the tool.
