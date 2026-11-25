package workspace

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrokerAllowGrants(t *testing.T) {
	cwd, scratch, outside := tree(t)
	g := grants(t, cwd, scratch, true)
	target := filepath.Join(outside, "secret.txt")

	b := NewBroker(g, nil, "S", time.Minute,
		func(context.Context, PermRequest) bool { return true }, nil)
	if err := b.Request(context.Background(), target, ModeRO, "need it"); err != nil {
		t.Fatalf("allow: %v", err)
	}
	if _, err := g.Check("", target, false); err != nil {
		t.Errorf("grant not applied: %v", err)
	}
	// second request: already granted → no ask
	asked := false
	b2 := NewBroker(g, nil, "S", time.Minute,
		func(context.Context, PermRequest) bool { asked = true; return true }, nil)
	if err := b2.Request(context.Background(), target, ModeRO, ""); err != nil || asked {
		t.Errorf("idempotent: err=%v asked=%v", err, asked)
	}
}

func TestBrokerDeny(t *testing.T) {
	cwd, scratch, outside := tree(t)
	g := grants(t, cwd, scratch, true)
	target := filepath.Join(outside, "secret.txt")
	b := NewBroker(g, nil, "S", time.Minute,
		func(context.Context, PermRequest) bool { return false }, nil)
	err := b.Request(context.Background(), target, ModeRW, "")
	if err == nil || !strings.Contains(err.Error(), "user denied") {
		t.Errorf("deny: %v", err)
	}
}

// P2-7 DoD: interrupt while pending → treated as denied-interrupted.
func TestBrokerInterruptWhilePending(t *testing.T) {
	cwd, scratch, outside := tree(t)
	g := grants(t, cwd, scratch, true)
	target := filepath.Join(outside, "secret.txt")
	ctx, cancel := context.WithCancel(context.Background())
	b := NewBroker(g, nil, "S", time.Hour, func(c context.Context, _ PermRequest) bool {
		<-c.Done() // the UI blocks until answered or cancelled
		return false
	}, nil)
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	err := b.Request(ctx, target, ModeRO, "")
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Errorf("interrupt-during-pending: %v", err)
	}
}

func TestBrokerTimeoutDenies(t *testing.T) {
	cwd, scratch, outside := tree(t)
	g := grants(t, cwd, scratch, true)
	target := filepath.Join(outside, "secret.txt")
	b := NewBroker(g, nil, "S", 60*time.Millisecond, func(c context.Context, _ PermRequest) bool {
		<-c.Done() // never answered
		return false
	}, nil)
	err := b.Request(context.Background(), target, ModeRO, "")
	if err == nil || !strings.Contains(err.Error(), "user did not respond") {
		t.Errorf("timeout: %v", err)
	}
}

func TestBrokerNoUIRefuses(t *testing.T) {
	cwd, scratch, outside := tree(t)
	g := grants(t, cwd, scratch, true)
	b := NewBroker(g, nil, "S", time.Minute, nil, nil)
	err := b.Request(context.Background(), filepath.Join(outside, "x"), ModeRO, "")
	if err == nil {
		t.Error("headless broker must refuse")
	}
}
