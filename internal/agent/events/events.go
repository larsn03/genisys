// Package events defines the typed event spine (SPEC §21). In P1 the CLI is
// the only subscriber; P3 persists these to the events table and streams
// them over WS. Keep this package dependency-free.
package events

// Event type names (SPEC §21).
const (
	StepStarted           = "step_started"
	TextDelta             = "text_delta"
	ToolStarted           = "tool_started"
	ToolOutput            = "tool_output"
	ToolFinished          = "tool_finished"
	Usage                 = "usage"
	StepFinished          = "step_finished"
	SessionStatus         = "session_status"
	InterruptStarted      = "interrupt_started"
	InterjectionDelivered = "interjection_delivered"
	FileChanged           = "file_changed"
	CheckpointCreated     = "checkpoint_created"
	CheckpointReverted    = "checkpoint_reverted"
	PermissionRequested   = "permission_requested"
	PermissionResolved    = "permission_resolved"
	UserTurn              = "user_turn"
	InterjectionQueued    = "interjection_queued"
	SessionError          = "session_error"
	TranscriptRepaired    = "transcript_repaired"
)

// Event is one spine record.
type Event struct {
	Type    string `json:"type"`
	StepID  string `json:"step_id,omitempty"`
	Payload any    `json:"payload,omitempty"`
}

// Sink receives events.
type Sink interface {
	Emit(Event)
}

// SinkFunc adapts a function to Sink.
type SinkFunc func(Event)

// Emit implements Sink.
func (f SinkFunc) Emit(e Event) { f(e) }

// Discard drops everything.
var Discard Sink = SinkFunc(func(Event) {})
