// Package perms implements request_readonly_access, request_read_write_access
// (SPEC §9.7) and get_scratchpad_folder (SPEC §9.6).
package perms

import (
	"context"
	"encoding/json"

	"github.com/larsn03/genisys/internal/tools"
	"github.com/larsn03/genisys/internal/workspace"
)

type requestTool struct{ rw bool }

// ReadOnlyAccess asks the user to allow reading a path outside the granted
// roots; blocks until answered (10-min timeout = deny).
func ReadOnlyAccess() tools.Tool { return requestTool{false} }

// ReadWriteAccess asks for write permission on a path.
func ReadWriteAccess() tools.Tool { return requestTool{true} }

func (t requestTool) Name() string {
	if t.rw {
		return "request_read_write_access"
	}
	return "request_readonly_access"
}

func (t requestTool) Description() string {
	if t.rw {
		return "Request read-write access to a path outside the working directory. Blocks until the user answers; on allow, later tools may write there. The path need not exist yet."
	}
	return "Request read-only access to a path outside the working directory. Blocks until the user answers; on allow, later tools may read it."
}

func (t requestTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"path":{"type":"string","description":"Absolute path (or relative to the working directory) to request access for."},
"reason":{"type":["string","null"],"description":"Short user-facing justification shown on the permission card."}},
"required":["path"],"additionalProperties":false}`)
}

func (t requestTool) Execute(ctx context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Path   string `json:"path"`
		Reason string `json:"reason"`
	}
	if err := tools.ParseArgs(raw, &a); err != nil {
		return tools.Result{Content: "invalid_json: " + err.Error(), IsError: true}
	}
	if a.Path == "" {
		return tools.Result{Content: "invalid_args: path is required", IsError: true}
	}
	mode := workspace.ModeRO
	if t.rw {
		mode = workspace.ModeRW
	}
	// resolve without requiring an existing grant (the request itself is
	// about widening the table)
	p := tools.Resolve(w.Cwd, a.Path)
	if w.Perms == nil {
		return tools.Result{Content: "permission denied: no UI available to ask the user", IsError: true}
	}
	if err := w.Perms.Request(ctx, p, mode, a.Reason); err != nil {
		return tools.Result{Content: "error: " + err.Error(), IsError: true}
	}
	return tools.Result{Content: "access granted (" + string(mode) + "): " + p}
}

// ScratchpadFolder returns the session scratchpad path (SPEC §9.6).
type ScratchpadFolder struct{}

func (ScratchpadFolder) Name() string { return "get_scratchpad_folder" }
func (ScratchpadFolder) Description() string {
	return "Return the absolute path of this session's scratchpad: the right place for intermediate files and generated artifacts (deliver_file_to_user moves finished ones into the workspace)."
}
func (ScratchpadFolder) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

func (ScratchpadFolder) Execute(_ context.Context, _ json.RawMessage, w *tools.World) tools.Result {
	if w.Scratchpad == "" {
		return tools.Result{Content: "error: no scratchpad for this session", IsError: true}
	}
	return tools.Result{Content: w.Scratchpad}
}
