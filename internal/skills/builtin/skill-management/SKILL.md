---
name: skill-management
description: Create, install from URL, and uninstall Genisys skills. Use when the user asks to add/save/reuse a workflow as a skill, install a skill from a URL, or remove one.
---

# Skill management

A Genisys skill is a folder containing `SKILL.md` with YAML frontmatter
(`name`, `description`) and a body of instructions. Skills live in
`~/.genisys/skills/<name>/SKILL.md` (user skills) or `<project>/skills/`
(project skills).

## Create a skill

1. Draft the SKILL.md: frontmatter `name` (lowercase slug, e.g.
   `weekly-report`), one-sentence `description` stating *when* to use it,
   then the step-by-step instructions the model should follow.
2. Ask the user for the name if unclear.
3. Write the file with `write_file` to the absolute path
   `~/.genisys/skills/<name>/SKILL.md` (call
   `request_read_write_access` first if the grant is refused).
4. Confirm the skill appears in `<available-skills>` next session; tell
   the user it is ready.

## Install from a URL

1. `web_extract` the raw URL (e.g. a GitHub raw SKILL.md) with objective
   "full file content".
2. Validate: starts with `---`, frontmatter has `name` and `description`.
   If validation fails, show the user what is wrong instead of installing.
3. Write it to `~/.genisys/skills/<name>/SKILL.md` as above. Supporting
   files referenced by the skill: fetch and place them next to SKILL.md.

## Uninstall

Delete the skill folder: `run_shell` with `rm -rf ~/.genisys/skills/<name>`
(permission-gated). Never delete built-in skills (skill-management,
documents-presentations-spreadsheets).
