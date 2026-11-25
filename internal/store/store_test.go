package store

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func openDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrateUpDown(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if v := db.SchemaVersion(); v != 1 {
		t.Errorf("version = %d", v)
	}
	// idempotent
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateDown(0); err != nil {
		t.Fatal(err)
	}
	if v := db.SchemaVersion(); v != 0 {
		t.Errorf("after down: version = %d", v)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	db := openDB(t)
	p := &Project{Name: "demo", Kind: "code", WorkspacePath: "/tmp/x", AllowCoding: true}
	if err := db.CreateProject(p); err != nil {
		t.Fatal(err)
	}
	s := &Session{ProjectID: p.ID, Title: "fix bug", ModelRef: "ep/m1", Persona: "coder"}
	if err := db.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSessionStatus(s.ID, StatusRunning); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetSession(s.ID)
	if err != nil || got.Status != StatusRunning {
		t.Fatalf("session = %+v err = %v", got, err)
	}
	// orphan sweep (P2-9)
	n, err := db.MarkOrphansInterrupted()
	if err != nil || n != 1 {
		t.Fatalf("orphans = %d, %v", n, err)
	}
	got, _ = db.GetSession(s.ID)
	if got.Status != StatusError {
		t.Errorf("orphan status = %s", got.Status)
	}
	// message round-trip
	m := map[string]any{"role": "user", "content": "hi"}
	b, _ := json.Marshal(m)
	if _, err := db.AppendMessage(s.ID, "user", b, "step_1"); err != nil {
		t.Fatal(err)
	}
	msgs, err := db.ListMessages(s.ID)
	if err != nil || len(msgs) != 1 || msgs[0].StepID != "step_1" {
		t.Fatalf("msgs = %+v, %v", msgs, err)
	}
	// events ledger
	if err := db.AppendEvent(s.ID, "step_1", "tool_started", map[string]any{"tool": "list_dir"}); err != nil {
		t.Fatal(err)
	}
	evs, err := db.ListEvents(s.ID)
	if err != nil || len(evs) != 1 || !strings.Contains(evs[0].PayloadJSON, "list_dir") {
		t.Fatalf("events = %+v, %v", evs, err)
	}
	// checkpoints + GC ledger
	for i := 0; i < 5; i++ {
		if _, err := db.AppendCheckpoint(p.ID, s.ID, "step_1", strings.Repeat("a", 40), "lbl"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.DeleteCheckpointsOlderThan(p.ID, 3); err != nil {
		t.Fatal(err)
	}
	cps, _ := db.ListCheckpoints(p.ID)
	if len(cps) != 3 {
		t.Errorf("checkpoints after GC = %d", len(cps))
	}
	// permissions
	id, err := db.RequestPermission(s.ID, "/etc/x", "ro")
	if err != nil {
		t.Fatal(err)
	}
	pend, _ := db.PendingPermissions(s.ID)
	if len(pend) != 1 {
		t.Errorf("pending = %v", pend)
	}
	if err := db.ResolvePermission(id, true); err != nil {
		t.Fatal(err)
	}
	gr, _ := db.GrantedPaths(s.ID)
	if gr["/etc/x"] != "ro" {
		t.Errorf("grants = %v", gr)
	}
}

func TestConcurrentWriters(t *testing.T) {
	db := openDB(t)
	s := &Session{ModelRef: "e/m"}
	if err := db.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b, _ := json.Marshal(map[string]any{"role": "user", "content": i})
			for k := 0; k < 20; k++ {
				if _, err := db.AppendMessage(s.ID, "user", b, ""); err != nil {
					t.Error(err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	msgs, err := db.ListMessages(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 320 {
		t.Errorf("messages = %d, want 320", len(msgs))
	}
}

func TestULID(t *testing.T) {
	a, b := NewID(), NewID()
	if len(a) != 26 {
		t.Fatalf("ulid len = %d (%q)", len(a), a)
	}
	if a == b {
		t.Error("ids collide")
	}
	// invalid chars
	for _, c := range a + b {
		if strings.ContainsRune("ILOU", c) {
			t.Errorf("bad crockford char %q", c)
		}
	}
}
