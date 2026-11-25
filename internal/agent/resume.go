package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/larsn03/genisys/internal/model"
	"github.com/larsn03/genisys/internal/store"
)

// SessionDir is the on-disk home of a session's transcript
// (SPEC §6.1: projects/<id>/sessions/<sid>/; free chats get
// <root>/sessions/<sid>/).
func SessionDir(root, projectID, sessionID string) string {
	if projectID != "" {
		return filepath.Join(root, "projects", projectID, "sessions", sessionID)
	}
	return filepath.Join(root, "sessions", sessionID)
}

// TranscriptPath is the canonical transcript location for a session.
func TranscriptPath(root, projectID, sessionID string) string {
	return filepath.Join(SessionDir(root, projectID, sessionID), "transcript.jsonl")
}

// Resume rebuilds a session from its transcript (SPEC §6.3): message
// envelopes are replayed, the system prompt will be re-rendered on the
// next request (never replayed). Torn tail lines are skipped.
func Resume(d Deps) (*Session, error) {
	if d.Transcript == nil && d.SessionID != "" {
		return nil, fmt.Errorf("agent: Resume needs Deps.Transcript (open %q first)", "transcript.jsonl")
	}
	s := NewSession(d)
	if d.Transcript == nil {
		return s, nil
	}
	path := d.Transcript.Path()
	entries, err := store.Replay(path)
	if err != nil {
		return nil, err
	}
	var lastErr string
	for _, e := range entries {
		if e.Kind != store.KindMessage {
			continue
		}
		var m model.Message
		if err := json.Unmarshal(e.Payload, &m); err != nil {
			lastErr = err.Error()
			continue
		}
		s.messages = append(s.messages, m) // fresh session: no contention
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	if lastErr != "" {
		s.emit("transcript_repair", map[string]string{"skipped": lastErr})
	}
	return s, nil
}
