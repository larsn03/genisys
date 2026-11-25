// Package model is the OpenAI-compatible endpoint client (SPEC §13):
// streaming chat/completions, SSE parsing, typed errors, usage capture.
// P1-1 scope; retry/backoff policy lands in P1-2, profiles in P1-3.
package model

import (
	"encoding/json"
	"fmt"
)

// ChatRequest is the POST /chat/completions body (SPEC §13.1).
// Pointer fields are omitted entirely when nil — some servers 400 on
// sampling params they don't implement.
type ChatRequest struct {
	Model           string         `json:"model"`
	Messages        []Message      `json:"messages"`
	Tools           []Tool         `json:"tools,omitempty"`
	ToolChoice      string         `json:"tool_choice,omitempty"`
	Stream          bool           `json:"stream"`
	StreamOptions   *StreamOptions `json:"stream_options,omitempty"`
	Temperature     *float64       `json:"temperature,omitempty"`
	TopP            *float64       `json:"top_p,omitempty"`
	MaxTokens       *int           `json:"max_tokens,omitempty"`
	ReasoningEffort *string        `json:"reasoning_effort,omitempty"`
}

// StreamOptions requests usage in the final SSE chunk (degrades silently).
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// Message is an OpenAI-shaped chat message. Content is either a plain string
// or a list of content parts (text/image); Parts wins when non-empty.
type Message struct {
	Role       string        `json:"role"`
	Content    string        `json:"-"`
	Parts      []ContentPart `json:"-"`
	Name       string        `json:"name,omitempty"`
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

// UnmarshalJSON accepts content as a plain string or a parts array
// (required for transcript replay and request echo-back).
func (m *Message) UnmarshalJSON(b []byte) error {
	var raw struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		Name       string          `json:"name,omitempty"`
		ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
		ToolCallID string          `json:"tool_call_id,omitempty"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	m.Role, m.Name, m.ToolCalls, m.ToolCallID = raw.Role, raw.Name, raw.ToolCalls, raw.ToolCallID
	if len(raw.Content) == 0 {
		return nil
	}
	var str string
	if err := json.Unmarshal(raw.Content, &str); err == nil {
		m.Content = str
		return nil
	}
	return json.Unmarshal(raw.Content, &m.Parts)
}

// ContentPart is one multimodal content block.
type ContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *ImageURLPart `json:"image_url,omitempty"`
}

// ImageURLPart carries a data: URL image (SPEC §9.6 attach_file flow).
type ImageURLPart struct {
	URL string `json:"url"`
}

// MarshalJSON emits content as string or parts array.
func (m Message) MarshalJSON() ([]byte, error) {
	out := struct {
		Role       string     `json:"role"`
		Content    any        `json:"content,omitempty"`
		Name       string     `json:"name,omitempty"`
		ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
		ToolCallID string     `json:"tool_call_id,omitempty"`
	}{Role: m.Role, Name: m.Name, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID}
	switch {
	case len(m.Parts) > 0:
		out.Content = m.Parts
	default:
		out.Content = m.Content
	}
	return json.Marshal(out)
}

// Tool is a function tool definition (schemas are raw JSON Schema, SPEC §9).
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is the function part of a tool definition.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolCall is a completed tool call as carried in transcripts.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction holds the name and raw JSON arguments.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// StreamKind enumerates the normalized stream events (IMPL §4).
type StreamKind string

const (
	KindTextDelta      StreamKind = "text_delta"
	KindReasoningDelta StreamKind = "reasoning_delta"
	KindToolCallDelta  StreamKind = "tool_call_delta"
	KindUsage          StreamKind = "usage"
	KindDone           StreamKind = "done"
)

// StreamEvent is one normalized piece of a completion stream.
type StreamEvent struct {
	Kind     StreamKind
	Text     string
	ToolCall *ToolCallDelta
	Usage    *Usage
}

// ToolCallDelta is one SSE tool-call fragment; reassemble by Index:
// first fragment carries ID+Name, later ones only Arguments.
type ToolCallDelta struct {
	Index     int    `json:"index"`
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"` // fragment
}

// Usage reports token counts (SPEC §21 usage event).
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Add accumulates usage counters in place.
func (u *Usage) Add(o Usage) {
	u.PromptTokens += o.PromptTokens
	u.CompletionTokens += o.CompletionTokens
	u.TotalTokens += o.TotalTokens
}

// --- wire chunk shapes (unexported; normalized into StreamEvents) ---------

type chatChunk struct {
	Choices []chunkChoice `json:"choices"`
	Usage   *Usage        `json:"usage,omitempty"`
}

type chunkChoice struct {
	Delta        chunkDelta `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

type chunkDelta struct {
	Content          string            `json:"content,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"` // common open-server extension
	ToolCalls        []json.RawMessage `json:"tool_calls,omitempty"`
}

func (d chunkDelta) toolCallDeltas() ([]ToolCallDelta, error) {
	out := make([]ToolCallDelta, 0, len(d.ToolCalls))
	for _, raw := range d.ToolCalls {
		var tc struct {
			Index    int    `json:"index"`
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		}
		if err := json.Unmarshal(raw, &tc); err != nil {
			return nil, fmt.Errorf("tool_call delta: %w", err)
		}
		out = append(out, ToolCallDelta{
			Index: tc.Index, ID: tc.ID, Type: tc.Type,
			Name: tc.Function.Name, Arguments: tc.Function.Arguments,
		})
	}
	return out, nil
}
