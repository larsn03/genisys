// Package ui implements the panel/delivery tools (SPEC §9.6). In server
// mode they emit events the web UI acts on; deliver_file_to_user performs
// the actual scratchpad → workspace copy through the WorkspaceWriter.
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/larsn03/genisys/internal/agent/events"
	"github.com/larsn03/genisys/internal/tools"
	"github.com/larsn03/genisys/internal/workspace"
)

// Tools returns the UI tool set for the server registry.
func Tools() []tools.Tool {
	return []tools.Tool{openFile{}, openURL{}, attachFile{}, deliverFile{}}
}

type openFile struct{}

func (openFile) Name() string { return "open_file_in_side_editor" }
func (openFile) Description() string {
	return "Open a file in the user's side editor panel. Do not auto-open code files the user is working on; do open generated documents with discretion."
}
func (openFile) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)
}
func (openFile) Execute(_ context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Path string `json:"path"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil || a.Path == "" {
		return tools.Result{Content: "invalid_args: path is required", IsError: true}
	}
	p, err := w.Access(a.Path, false)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	w.Emit.Emit(events.Event{Type: "ui_open_file", Payload: map[string]any{"path": p}})
	return tools.Result{Content: "opened " + filepath.Base(p) + " in the side editor"}
}

type openURL struct{}

func (openURL) Name() string { return "open_url_in_app_browser" }
func (openURL) Description() string {
	return "Open a URL in the in-app browser tab (localhost, file://, or allow-listed origins). An identical open tab is focused+refreshed instead of duplicated."
}
func (openURL) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false}`)
}
func (openURL) Execute(_ context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		URL string `json:"url"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil || a.URL == "" {
		return tools.Result{Content: "invalid_args: url is required", IsError: true}
	}
	u, err := url.Parse(a.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "file") {
		return tools.Result{Content: "error: url must be http(s) or file://", IsError: true}
	}
	if u.Scheme != "file" && !isLocalhost(u.Hostname()) && !originAllowed(u, w.BrowserOrigins) {
		return tools.Result{Content: "error: origin " + u.Scheme + "://" + u.Host +
			" is not localhost and not in browser.allowed_origins — ask the user to add it in Settings", IsError: true}
	}
	w.Emit.Emit(events.Event{Type: "ui_open_url", Payload: map[string]any{"url": a.URL}})
	return tools.Result{Content: "opened " + a.URL}
}

// originAllowed matches "scheme://host[:port]" entries from
// browser.allowed_origins (SPEC §5.4).
func originAllowed(u *url.URL, allowed []string) bool {
	want := u.Scheme + "://" + u.Host
	for _, a := range allowed {
		if strings.TrimRight(a, "/") == want {
			return true
		}
	}
	return false
}

func isLocalhost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".localhost")
}

type attachFile struct{}

func (attachFile) Name() string { return "attach_file" }
func (attachFile) Description() string {
	return "Register an image with the session so the user sees it; returns a markdown snippet to reference it. Remote URLs must be downloaded first."
}
func (attachFile) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"path":{"type":"string"},"alt_text":{"type":["string","null"]}},
"required":["path"],"additionalProperties":false}`)
}
func (attachFile) Execute(_ context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Path    string `json:"path"`
		AltText string `json:"alt_text"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil || a.Path == "" {
		return tools.Result{Content: "invalid_args: path is required", IsError: true}
	}
	p, err := w.Access(a.Path, false)
	if err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	if _, err := os.Stat(p); err != nil {
		return tools.Result{Content: "error: file not found", IsError: true}
	}
	w.Emit.Emit(events.Event{Type: "ui_attach_file", Payload: map[string]any{"path": p, "alt": a.AltText}})
	alt := a.AltText
	if alt == "" {
		alt = filepath.Base(p)
	}
	return tools.Result{Content: fmt.Sprintf("![%s](%s)", alt, p)}
}

type deliverFile struct{}

func (deliverFile) Name() string { return "deliver_file_to_user" }
func (deliverFile) Description() string {
	return "Move a finished artifact from the scratchpad into the user's visible area (the only sanctioned way). target_path is relative to the working directory unless absolute."
}
func (deliverFile) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"source_path":{"type":"string","description":"File in the scratchpad (or workspace)"},
"target_path":{"type":"string","description":"Destination path in the working directory"},
"overwrite":{"type":"boolean"}},
"required":["source_path","target_path","overwrite"],"additionalProperties":false}`)
}

func (deliverFile) Execute(ctx context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		SourcePath string `json:"source_path"`
		TargetPath string `json:"target_path"`
		Overwrite  bool   `json:"overwrite"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil || a.SourcePath == "" || a.TargetPath == "" {
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
		return tools.Result{Content: "error: no workspace writer wired", IsError: true}
	}
	if _, err := os.Stat(src); err != nil {
		return tools.Result{Content: "error: source does not exist", IsError: true}
	}
	if _, err := os.Stat(dst); err == nil && !a.Overwrite {
		return tools.Result{Content: "error: target exists (set overwrite to replace)", IsError: true}
	}
	if err := w.Writer.Mutate(ctx, w.StepID, fmt.Sprintf("before deliver_file_to_user(%s)", filepath.Base(dst)),
		[]string{dst}, func(t *workspace.Txn) error {
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			in, err := os.Open(src)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			defer out.Close()
			_, err = io.Copy(out, in)
			return err
		}); err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	w.Emit.Emit(events.Event{Type: "ui_deliver_file", Payload: map[string]any{"path": dst}})
	return tools.Result{Content: "delivered " + filepath.Base(dst) + " to the user at " + dst}
}
