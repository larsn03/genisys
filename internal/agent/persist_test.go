package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/larsn03/genisys/internal/model"
	"github.com/larsn03/genisys/internal/store"
)

// P2-2 DoD: kill -9 mid-session → resume continues from the last persisted
// tool result. We simulate the kill by opening the transcript, running a
// tool step, and abandoning the process state (no Close, fresh Session).
func TestResumeAfterCrash(t *testing.T) {
	dir := t.TempDir()
	tpath := filepath.Join(dir, "transcript.jsonl")
	tr, err := store.OpenTranscript(tpath)
	if err != nil {
		t.Fatal(err)
	}
	m1 := newMock(t, sseToolCall("c1", "list_dir", `{"path": ""}`))
	s1 := NewSession(Deps{
		Client: m1.client(t), Model: "test-model", Tools: fixtureRegistry(),
		MaxSteps: 8, Cwd: fixtureCwd(), Scratchpad: t.TempDir(),
		SessionID: "S1", Transcript: tr,
		Retry: &model.RetryPolicy{MaxAttempts: 1, Base: time.Millisecond},
	})
	// drive one step manually: Run would loop until the mock repeats the
	// tool call forever, so cancel right after the tool result lands.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _, _ = s1.Run(ctx, "list"); close(done) }()
	for len(s1.Messages()) < 3 { // user, assistant+call, tool
	}
	cancel()
	<-done
	// "crash": abandon tr without Close; everything through the tool
	// result must already be fsynced.

	entries, err := store.Replay(tpath)
	if err != nil {
		t.Fatal(err)
	}
	var msgs []model.Message
	for _, e := range entries {
		if e.Kind == store.KindMessage {
			var m model.Message
			if err := json.Unmarshal(e.Payload, &m); err == nil {
				msgs = append(msgs, m)
			}
		}
	}
	if len(msgs) < 3 || msgs[2].Role != "tool" {
		t.Fatalf("persisted messages = %+v", msgs)
	}

	// resume: fresh transcript handle + session, mock now answers text
	m2 := newMock(t, sseText("resumed fine"))
	tr2, err := store.OpenTranscript(tpath)
	if err != nil {
		t.Fatal(err)
	}
	defer tr2.Close()
	s2, err := Resume(Deps{
		Client: m2.client(t), Model: "test-model", Tools: fixtureRegistry(),
		MaxSteps: 8, Cwd: fixtureCwd(), Scratchpad: t.TempDir(),
		SessionID: "S1", Transcript: tr2,
		Retry: &model.RetryPolicy{MaxAttempts: 1, Base: time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.Messages()) != len(msgs) {
		t.Fatalf("resume rebuilt %d msgs, transcript has %d", len(s2.Messages()), len(msgs))
	}
	got, err := s2.Run(context.Background(), "continue")
	if err != nil {
		t.Fatal(err)
	}
	if got != "resumed fine" {
		t.Errorf("resumed run = %q", got)
	}
	// the resumed request must carry the pre-crash tool result
	if !m2.messagesContain(0, "D ignored") {
		t.Error("resumed context lost the tool result")
	}
}

// P2-9: a torn final line (crash mid-write) is skipped, not fatal.
func TestResumeTornTail(t *testing.T) {
	dir := t.TempDir()
	tpath := filepath.Join(dir, "transcript.jsonl")
	tr, err := store.OpenTranscript(tpath)
	if err != nil {
		t.Fatal(err)
	}
	_ = tr.Append(store.KindMessage, "", model.Message{Role: "user", Content: "hello"})
	tr.Close()
	// simulate torn write
	f, _ := os.OpenFile(tpath, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"kind":"message","payload":{"role":"assistant","con`)
	f.Close()

	s, err := Resume(Deps{Transcript: mustOpen(t, tpath), Model: "m", Tools: fixtureRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Messages()) != 1 || s.Messages()[0].Content != "hello" {
		t.Errorf("resumed = %+v", s.Messages())
	}
}

func mustOpen(t *testing.T, p string) *store.Transcript {
	t.Helper()
	tr, err := store.OpenTranscript(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.Close() })
	return tr
}

// P2-9: with a DB wired, every message lands in the messages table.
func TestDBMirror(t *testing.T) {
	db := openStoreDB(t)
	sess := &store.Session{ID: "S2", ModelRef: "e/m"}
	if err := db.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	m := newMock(t,
		sseToolCall("c1", "list_dir", `{"path": ""}`),
		sseText("done"))
	s := NewSession(Deps{
		Client: m.client(t), Model: "test-model", Tools: fixtureRegistry(),
		MaxSteps: 8, Cwd: fixtureCwd(), Scratchpad: t.TempDir(),
		SessionID: "S2", DB: db,
		Retry: &model.RetryPolicy{MaxAttempts: 1, Base: time.Millisecond},
	})
	if _, err := s.Run(context.Background(), "list"); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListMessages("S2")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 { // user, assistant, tool, assistant
		t.Fatalf("db messages = %d: %+v", len(rows), rows)
	}
	if rows[2].Role != "tool" || !strings.Contains(rows[2].ContentJSON, "D ignored") {
		t.Errorf("tool row = %+v", rows[2])
	}
	got, _ := db.GetSession("S2")
	if got.Status != store.StatusDone {
		t.Errorf("status after done = %s", got.Status)
	}
}

func openStoreDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
