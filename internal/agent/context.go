// Context management (SPEC §24): tool-result aging, compaction at 80%
// of the context window (structured summary + user-visible marker), and
// overflow auto-compact-retry (SPEC §23).

package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/larsn03/genisys/internal/model"
)

// Compaction policy (SPEC §24).
const (
	compactFraction = 0.80 // compact at 80% of the context window
	compactTail     = 6    // recent messages kept verbatim
	ageKeepTail     = 12   // tool results inside the last N messages stay whole
	summaryPrefix   = "[Context summary — earlier conversation was compacted. Facts below are historical.]\n"
	agedPlaceholder = "[tool result omitted to save context — re-run the tool if needed]"
)

// KindCompact marks a transcript entry that replaces history with a
// summary + tail (SPEC §6.3 replay applies it like compaction did live).
const KindCompact = "compact"

//go:embed templates/summarize.tmpl
var summarizeTmplText string

// ageToolResults replaces stale tool payloads with placeholders, keeping
// call/result pairing valid (SPEC §24).
func ageToolResults(msgs []model.Message) []model.Message {
	if len(msgs) <= ageKeepTail {
		return msgs
	}
	out := make([]model.Message, len(msgs))
	copy(out, msgs)
	for i := 0; i < len(out)-ageKeepTail; i++ {
		if out[i].Role == "tool" && len(out[i].Content) > len(agedPlaceholder) {
			out[i].Content = agedPlaceholder
		}
	}
	return out
}

// requestAged snapshots the transcript with aging applied (used for both
// live requests and the summarization call).
func (s *Session) requestAged() []model.Message {
	s.mu.Lock()
	msgs := append([]model.Message(nil), s.messages...)
	s.mu.Unlock()
	return ageToolResults(msgs)
}

// overLimit reports whether the last measured prompt crossed the 80%
// compaction line (SPEC §24).
func (s *Session) overLimit() bool {
	if s.d.ContextWindow <= 0 {
		return false
	}
	return s.lastPromptTokens >= int(float64(s.d.ContextWindow)*compactFraction)
}

// maybeCompact compacts when the meter crossed the line; returns whether
// compaction happened.
func (s *Session) maybeCompact(ctx context.Context) bool {
	if !s.overLimit() {
		return false
	}
	return s.Compact(ctx, "threshold")
}

// CompactNow is the manual action (error card / REST).
func (s *Session) CompactNow(ctx context.Context) error {
	if !s.Compact(ctx, "manual") {
		return fmt.Errorf("agent: nothing to compact (or summarization failed)")
	}
	return nil
}

// Compact replaces history with [summary] + a boundary-safe tail
// (SPEC §24). Persisted as a KindCompact transcript entry so resume
// reconstructs exactly the same state.
func (s *Session) Compact(ctx context.Context, reason string) bool {
	s.mu.Lock()
	full := append([]model.Message(nil), s.messages...)
	s.mu.Unlock()
	if len(full) < compactTail+2 {
		return false
	}
	start := len(full) - compactTail
	for start > 0 && full[start].Role == "tool" { // never orphan a tool result
		start--
	}
	if start <= 1 {
		return false // nothing meaningful to summarize
	}
	head, tail := full[:start], full[start:]

	before := s.lastPromptTokens
	summary, err := s.summarize(ctx, head)
	if err != nil || strings.TrimSpace(summary) == "" {
		return false
	}
	summaryMsg := model.Message{Role: "user", Content: summaryPrefix + summary}
	s.mu.Lock()
	s.messages = append([]model.Message{summaryMsg}, tail...)
	s.mu.Unlock()

	if t := s.d.Transcript; t != nil {
		_ = t.Append(KindCompact, "", map[string]any{
			"summary": summaryMsg.Content,
			"tail":    tail,
		})
	}
	after := estimateTokens(append([]model.Message{summaryMsg}, tail...)...)
	s.lastPromptTokens = after
	s.emit("context_compacted", map[string]any{
		"reason": reason, "before_tokens": before, "after_tokens": after,
		"summarized": len(head), "kept": len(tail),
	})
	return true
}

// summarize runs the structured-summary sub-call on the same endpoint
// (SPEC §24): goal, decisions, files touched, open threads, last steps.
func (s *Session) summarize(ctx context.Context, head []model.Message) (string, error) {
	msgs := ageToolResults(head)
	req := model.ChatRequest{
		Model:    s.d.Model,
		Messages: append([]model.Message{{Role: "system", Content: summarizeTmplText}}, msgs...),
	}
	// keep the summarization call itself inside the window: clip payloads
	for i := range req.Messages {
		req.Messages[i].Content = clipRunes(req.Messages[i].Content, 3000)
	}
	cctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	comp, err := s.d.Client.CompleteWith(cctx, req, s.d.Retry)
	if err != nil {
		return "", err
	}
	return comp.Text, nil
}

// isOverflow classifies endpoint length/context 4xx errors (SPEC §23
// "Context overflow (endpoint 400 on length)").
func isOverflow(err error) bool {
	var ae *model.APIError
	if !errors.As(err, &ae) {
		return false
	}
	if ae.Status != 400 && ae.Status != 413 {
		return false
	}
	m := strings.ToLower(ae.Message)
	for _, k := range []string{"context", "length", "token", "prompt", "max_tokens", "too long"} {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

// estimateTokens is a coarse 4-chars-per-token estimate used until the
// next usage event recalibrates.
func estimateTokens(msgs ...model.Message) int {
	total := 0
	for _, m := range msgs {
		total += len(m.Content) / 4
		for _, tc := range m.ToolCalls {
			total += len(tc.Function.Arguments) / 4
		}
	}
	return total
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n…[clipped]"
}

// applyCompactEntry rebuilds session state at a KindCompact entry during
// resume — identical to what Compact did live.
func applyCompactEntry(s *Session, payload json.RawMessage) {
	var e struct {
		Summary string          `json:"summary"`
		Tail    []model.Message `json:"tail"`
	}
	if err := json.Unmarshal(payload, &e); err != nil || e.Summary == "" {
		return
	}
	s.messages = append([]model.Message{{Role: "user", Content: e.Summary}}, e.Tail...)
}
