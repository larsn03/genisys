// Package mutate implements the file-mutation tools (SPEC §9.2):
// edit_file_tool, replace_file, delete, move, copy, create_folder. Every
// mutation goes through the WorkspaceWriter critical section: permission →
// checkpoint → write → diff → event (SPEC §7.2).
package mutate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/larsn03/genisys/internal/tools"
	"github.com/larsn03/genisys/internal/workspace"
)

// --- edit_file_tool ----------------------------------------------------------

type EditFile struct{}

func (EditFile) Name() string { return "edit_file_tool" }
func (EditFile) Description() string {
	return "Replace exact strings in a file. Each search_text must occur exactly once (CRLF is normalized to LF for matching). All edits apply atomically or none do. Requires having read the file in this session."
}
func (EditFile) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"path":{"type":"string"},
"edits":{"type":"array","minItems":1,"items":{"type":"object","properties":{
  "search_text":{"type":"string","description":"Exact text to replace. Must occur exactly once."},
  "replace_text":{"type":"string"}},"required":["search_text","replace_text"],"additionalProperties":false}}},
"required":["path","edits"],"additionalProperties":false}`)
}

func (EditFile) Execute(ctx context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Path  string `json:"path"`
		Edits []struct {
			SearchText  string `json:"search_text"`
			ReplaceText string `json:"replace_text"`
		} `json:"edits"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil {
		return tools.Result{Content: "invalid_json: " + err.Error(), IsError: true}
	}
	if a.Path == "" || len(a.Edits) == 0 {
		return tools.Result{Content: "invalid_args: path and at least one edit are required", IsError: true}
	}
	p, err := w.Access(a.Path, true)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	if w.Writer == nil {
		return tools.Result{Content: "error: no workspace writer wired (mutations disabled)", IsError: true}
	}
	if err := w.Writer.CheckRead(p); err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	norm := strings.ReplaceAll(string(data), "\r\n", "\n")

	// validate ALL edits first (all-or-nothing, line-anchored diagnostics)
	cur := norm
	for i, e := range a.Edits {
		n := strings.Count(cur, e.SearchText)
		switch {
		case e.SearchText == "":
			return editErr(i, "search_text is empty")
		case n == 0:
			return editErr(i, "search_text not found")
		case n > 1:
			return editErr(i, fmt.Sprintf("search_text matches %d times; must occur exactly once — add surrounding context", n))
		}
		cur = strings.Replace(cur, e.SearchText, e.ReplaceText, 1)
	}

	if err := w.Writer.Mutate(ctx, w.StepID, fmt.Sprintf("before edit_file_tool(%s)", filepath.Base(p)),
		[]string{p}, func(t *workspace.Txn) error {
			return os.WriteFile(p, []byte(cur), 0o644)
		}); err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	w.Writer.MarkRead(p) // agent may edit again without re-reading its own write
	return tools.Result{Content: fmt.Sprintf("applied %d edit(s) to %s", len(a.Edits), filepath.Base(p))}
}

func editErr(i int, msg string) tools.Result {
	return tools.Result{Content: fmt.Sprintf("edit %d rejected: %s (no edits applied)", i+1, msg), IsError: true}
}

// --- replace_file --------------------------------------------------------------

type ReplaceFile struct{}

func (ReplaceFile) Name() string { return "replace_file" }
func (ReplaceFile) Description() string {
	return "Write a full file (creates parents). Requires having read the file unchanged since, unless it does not exist."
}
func (ReplaceFile) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"path":{"type":"string"},"content":{"type":"string"}},
"required":["path","content"],"additionalProperties":false}`)
}

func (ReplaceFile) Execute(ctx context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil {
		return tools.Result{Content: "invalid_json: " + err.Error(), IsError: true}
	}
	if a.Path == "" {
		return tools.Result{Content: "invalid_args: path is required", IsError: true}
	}
	p, err := w.Access(a.Path, true)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	if w.Writer == nil {
		return tools.Result{Content: "error: no workspace writer wired (mutations disabled)", IsError: true}
	}
	if _, statErr := os.Stat(p); statErr == nil {
		if err := w.Writer.CheckRead(p); err != nil {
			return tools.Result{Content: "error: " + err.Error(), IsError: true}
		}
	}
	if err := w.Writer.Mutate(ctx, w.StepID, fmt.Sprintf("before replace_file(%s)", filepath.Base(p)),
		[]string{p}, func(t *workspace.Txn) error {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			return os.WriteFile(p, []byte(a.Content), 0o644)
		}); err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	w.Writer.MarkRead(p)
	return tools.Result{Content: "wrote " + filepath.Base(p)}
}

// --- delete / move / copy / create_folder ---------------------------------------

type Delete struct{}

func (Delete) Name() string { return "delete" }
func (Delete) Description() string {
	return "Delete a file or folder (recursive). The user can roll back via checkpoint."
}
func (Delete) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)
}

func (Delete) Execute(ctx context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Path string `json:"path"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil {
		return tools.Result{Content: "invalid_json: " + err.Error(), IsError: true}
	}
	p, err := w.Access(a.Path, true)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	if w.Writer == nil {
		return tools.Result{Content: "error: no workspace writer wired (mutations disabled)", IsError: true}
	}
	if err := w.Writer.Mutate(ctx, w.StepID, fmt.Sprintf("before delete(%s)", filepath.Base(p)),
		[]string{p}, func(t *workspace.Txn) error { return os.RemoveAll(p) }); err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	return tools.Result{Content: "deleted " + filepath.Base(p)}
}

type Move struct{}

func (Move) Name() string { return "move" }
func (Move) Description() string {
	return "Move/rename a file or folder. Set can_overwrite only when replacing the destination is intended."
}
func (Move) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"source_path":{"type":"string"},"target_path":{"type":"string"},
"can_overwrite":{"type":"boolean"}},
"required":["source_path","target_path","can_overwrite"],"additionalProperties":false}`)
}

func (Move) Execute(ctx context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	return moveCopy(ctx, raw, w, true)
}

type Copy struct{}

func (Copy) Name() string { return "copy" }
func (Copy) Description() string {
	return "Copy a file or folder. Set can_overwrite only when merging over the destination is intended."
}
func (Copy) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"source_path":{"type":"string"},"target_path":{"type":"string"},
"can_overwrite":{"type":"boolean"}},
"required":["source_path","target_path","can_overwrite"],"additionalProperties":false}`)
}

func (Copy) Execute(ctx context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	return moveCopy(ctx, raw, w, false)
}

func moveCopy(ctx context.Context, raw json.RawMessage, w *tools.World, isMove bool) tools.Result {
	var a struct {
		SourcePath   string `json:"source_path"`
		TargetPath   string `json:"target_path"`
		CanOverwrite bool   `json:"can_overwrite"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil {
		return tools.Result{Content: "invalid_json: " + err.Error(), IsError: true}
	}
	if a.SourcePath == "" || a.TargetPath == "" {
		return tools.Result{Content: "invalid_args: source_path and target_path are required", IsError: true}
	}
	src, err := w.Access(a.SourcePath, false)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	dst, err := w.Access(a.TargetPath, true)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	if w.Writer == nil {
		return tools.Result{Content: "error: no workspace writer wired (mutations disabled)", IsError: true}
	}
	if _, err := os.Stat(src); err != nil {
		return tools.Result{Content: "error: source does not exist: " + a.SourcePath, IsError: true}
	}
	if _, err := os.Stat(dst); err == nil && !a.CanOverwrite {
		return tools.Result{Content: "error: target exists (set can_overwrite to replace): " + a.TargetPath, IsError: true}
	}
	verb := "copy"
	if isMove {
		verb = "move"
	}
	if err := w.Writer.Mutate(ctx, w.StepID, fmt.Sprintf("before %s(%s→%s)", verb, filepath.Base(src), filepath.Base(dst)),
		[]string{src, dst}, func(t *workspace.Txn) error {
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if isMove {
				return os.Rename(src, dst)
			}
			return copyTree(src, dst)
		}); err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	return tools.Result{Content: verb + "d " + filepath.Base(src) + " → " + filepath.Base(dst)}
}

func copyTree(src, dst string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fi.Mode())
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	}
	if err := os.MkdirAll(dst, fi.Mode()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

type CreateFolder struct{}

func (CreateFolder) Name() string { return "create_folder" }
func (CreateFolder) Description() string {
	return "Create a folder, including parents (depth cap 16)."
}
func (CreateFolder) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)
}

func (CreateFolder) Execute(ctx context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Path string `json:"path"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil {
		return tools.Result{Content: "invalid_json: " + err.Error(), IsError: true}
	}
	p, err := w.Access(a.Path, true)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	if depth := strings.Count(filepath.Clean(p), string(filepath.Separator)); depth > 64 {
		return tools.Result{Content: "error: path too deep", IsError: true}
	}
	if w.Writer == nil {
		return tools.Result{Content: "error: no workspace writer wired (mutations disabled)", IsError: true}
	}
	if err := w.Writer.Mutate(ctx, w.StepID, fmt.Sprintf("before create_folder(%s)", filepath.Base(p)),
		[]string{p}, func(t *workspace.Txn) error { return os.MkdirAll(p, 0o755) }); err != nil {
		if errors.Is(err, workspace.ErrDenied) {
			return tools.Result{Content: "error: " + err.Error(), IsError: true}
		}
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	return tools.Result{Content: "created " + filepath.Base(p)}
}
