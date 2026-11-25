---
name: file-history
description: Undo Genisys workspace changes — list checkpoints, roll a single file or a whole step back. Use when the user says "undo", "that change was wrong", "restore the old file", "what did you change", or asks to revert to an earlier state.
---

# File history & undo

Every agent mutation to the workspace is checkpointed BEFORE it happens
(shadow git repo — the user's own `.git` is never touched). Two undo
granularities exist:

1. **Per step** — the UI "Revert step" button; you trigger the same via
   the checkpoint ledger.
2. **Per file** — restore one file to one snapshot.

## Recipes

- **"What did you change?"**
  `genisys_tool {"action":"checkpoint_list"}` → sha, step, label
  ("before replace_file(index.html)"). Labels name the exact mutation.
- **"Restore index.html to how it was before that edit"**
  `{"action":"file_history","path":"index.html"}` → snapshot timeline
  (newest first; snapshots capture the state BEFORE each change), pick
  the sha whose label matches, then
  `{"action":"file_restore","path":"index.html","sha":"<sha>"}`.
- **User-edited files**: file_restore refuses (error mentions "user
  edits") when the file on disk changed outside the agent since the last
  checkpoint. Ask the user before passing `"force":true`.
- **Whole step**: restoring every file listed under one checkpoint's
  label — loop file_restore over the paths from file_history, or point
  the user at the Revert step button in the UI.

## Rules

- Snapshots are PRE-mutation: the sha captured "before edit X" is the
  state you want when undoing X.
- Restores are themselves mutations → checkpointed, so undo-of-undo
  works. Reassure the user.
- Never `git` in the user's repo to undo agent work; use checkpoints —
  the user may have uncommitted work of their own.
