---
name: documents-presentations-spreadsheets
description: Produce office deliverables — memos (docx/pdf), slide decks (pptx), spreadsheets (xlsx) — using run_python and paperworks. Use whenever the user asks for a document, deck, sheet, or redline to be delivered.
---

# Documents, presentations, spreadsheets

Generate files with `run_python` (sandboxed Python, no system deps). The
`paperworks` library is preinstalled in the sandbox:

- `ppt_builder.Deck` — decks: `Deck(title)`, `add_title`, `add_bullets`,
  `add_two_col`, `add_assumptions`, `save('/outputs/deck.pptx')`.
- `pdf_simple.write_pdf(path, title, sections)` — clean text-first PDFs;
  sections = list of `(heading, [lines])` or plain strings.
- `docx_redline` — tracked-change edits on existing .docx;
  `read_docx_text(path)` to read one.
- `xlsx_helpers.write_xlsx(path, sheets)` / `read_xlsx(path)` — sheets is
  `{name: [[row cells...], ...]}`.

Rules:

1. Write deliverables to `/outputs/<name>.<ext>` only; staged inputs are
   read-only under `/inputs/<name>` (pass `stage_files` with scratchpad or
   workspace paths).
2. Every deck/document ends with an **Assumptions** slide/section listing
   anything invented or estimated.
3. After `run_python` succeeds, `deliver_file_to_user` each output (use
   the absolute scratchpad paths it reports) so the user gets the file.
4. Read attached .docx/.xlsx/.pdf via `load_file_attachment` first; for
   formats it cannot inline, use `run_python` with paperworks readers.
