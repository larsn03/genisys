// transcript.go — the resumable source of truth (SPEC §6.3): one JSON
// envelope per line, fsynced per line so a kill -9 loses nothing that was
// acknowledged. Resume rebuilds the message list; the system prompt is
// re-rendered, never replayed.
package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// Transcript kinds.
const (
	KindMessage    = "message"    // payload: OpenAI-format message
	KindStepStart  = "step_start" // payload: {step, model}
	KindUsage      = "usage"      // payload: token counts
	KindCheckpoint = "checkpoint" // payload: {sha, label}
	KindMeta       = "meta"       // payload: session metadata updates
)

// Entry is one transcript line.
type Entry struct {
	Kind    string          `json:"kind"`
	StepID  string          `json:"step_id,omitempty"`
	TS      string          `json:"ts"`
	Payload json.RawMessage `json:"payload"`
}

// Transcript appends envelopes to a .jsonl file. Safe for concurrent use.
type Transcript struct {
	mu sync.Mutex
	f  *os.File
}

// OpenTranscript opens (creating parents) the transcript for appending.
func OpenTranscript(path string) (*Transcript, error) {
	if dir := strings.TrimSuffix(path, "/transcript.jsonl"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Transcript{f: f}, nil
}

// Append writes one envelope and fsyncs (SPEC §6.3: fsync per line).
func (t *Transcript) Append(kind, stepID string, payload any) error {
	pj, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	line, err := json.Marshal(Entry{Kind: kind, StepID: stepID, TS: nowStamp(), Payload: pj})
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := t.f.Write(append(line, '\n')); err != nil {
		return err
	}
	return t.f.Sync()
}

// Close flushes the file.
func (t *Transcript) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.f.Close()
}

// Replay reads the whole transcript. A truncated final line (crash
// mid-write) is ignored, not fatal.
func Replay(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		s := strings.TrimSpace(sc.Text())
		if s == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(s), &e); err != nil {
			// tolerate a torn tail; anything else is corruption
			if _, perr := f.Seek(0, io.SeekEnd); perr == nil {
				continue
			}
			return out, fmt.Errorf("transcript line %d: %w", line, err)
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// Path returns the transcript file path.
func (t *Transcript) Path() string { return t.f.Name() }
