// broker.go — the permission flow (SPEC §9.7/§10): tools ask the Broker
// when a path is outside the grant table; the Broker persists a pending row
// (permissions table, granted NULL), asks the UI (CLI prompt / WS card),
// and applies the outcome. Timeout (10 min) = deny "user did not respond";
// an interrupt while pending = deny "interrupted" (SPEC §7.4).
package workspace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/larsn03/genisys/internal/agent/events"
	"github.com/larsn03/genisys/internal/store"
)

// PermTimeoutDefault is the SPEC §9.7 default (10 min).
const PermTimeoutDefault = 10 * time.Minute

// PermRequest is one pending permission request.
type PermRequest struct {
	ID     int64
	Path   string
	Mode   Mode
	Reason string
}

// Asker renders the request to the user and blocks for the answer.
// Returning false means deny (reason carried by the caller's context or
// timeout). Implementations must honor ctx cancellation.
type Asker func(ctx context.Context, req PermRequest) bool

// Broker gates FS access beyond the granted roots.
type Broker struct {
	grants    *Grants
	db        *store.DB
	sessionID string
	timeout   time.Duration
	ask       Asker
	emit      events.Sink
}

// NewBroker wires a Broker; ask may be nil (headless: always deny with a
// "no UI to ask" reason, which keeps the model honest).
func NewBroker(g *Grants, db *store.DB, sessionID string, timeout time.Duration, ask Asker, emit events.Sink) *Broker {
	if timeout <= 0 {
		timeout = PermTimeoutDefault
	}
	if emit == nil {
		emit = events.Discard
	}
	return &Broker{grants: g, db: db, sessionID: sessionID, timeout: timeout, ask: ask, emit: emit}
}

// ErrPermDenied reports a user/timeout denial.
var ErrPermDenied = errors.New("permission denied")

// Request asks for `mode` on `path` and grants it on allow. Idempotent: an
// already-covered grant returns nil without asking.
func (b *Broker) Request(ctx context.Context, path string, mode Mode, reason string) error {
	if b == nil {
		return fmt.Errorf("%w: no permission UI available", ErrPermDenied)
	}
	// already granted?
	if _, err := b.grants.Check("", path, mode == ModeRW); err == nil {
		return nil
	}
	var id int64
	if b.db != nil && b.sessionID != "" {
		var err error
		id, err = b.db.RequestPermission(b.sessionID, path, string(mode))
		if err != nil {
			return err
		}
	}
	req := PermRequest{ID: id, Path: path, Mode: mode, Reason: reason}
	b.emit.Emit(events.Event{Type: events.PermissionRequested, Payload: map[string]any{
		"id": id, "path": display(path), "mode": string(mode), "reason": reason,
	}})

	ctx2, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	allowed := false
	if b.ask != nil {
		allowed = b.ask(ctx2, req)
	}

	denial := ""
	switch {
	case allowed:
	case ctx.Err() != nil:
		denial = "interrupted" // SPEC §7.4: pending card dismissed on interrupt
	case ctx2.Err() != nil:
		denial = "user did not respond"
	default:
		denial = "user denied"
	}
	if b.db != nil && id != 0 {
		_ = b.db.ResolvePermission(id, allowed)
	}
	b.emit.Emit(events.Event{Type: events.PermissionResolved, Payload: map[string]any{
		"id": id, "path": display(path), "mode": string(mode), "allowed": allowed, "reason": denial,
	}})
	if !allowed {
		return fmt.Errorf("%w: %s access to %q (%s)", ErrPermDenied, mode, display(path), denial)
	}
	return b.grants.Grant(path, mode)
}
