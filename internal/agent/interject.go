package agent

import (
	"sync"
)

// InterjectionMode: interrupt (steer now) vs queue (must read later),
// SPEC §7.4.
type InterjectionMode string

// Modes.
const (
	ModeQueue     InterjectionMode = "queue"
	ModeInterrupt InterjectionMode = "interrupt"
)

// Interjection is one user message sent while the session is running.
type Interjection struct {
	ID   string
	Text string
	Mode InterjectionMode
}

// Interjector is the session's interjection queue + interrupt signal
// (SPEC §7.4). Safe for concurrent use; the CLI/WS layer Push(), the loop
// drains at delivery boundaries and watches Notify().
type Interjector struct {
	mu     sync.Mutex
	queue  []Interjection
	notify chan struct{}
	seq    int
}

// NewInterjector builds an empty queue.
func NewInterjector() *Interjector {
	return &Interjector{notify: make(chan struct{}, 1)}
}

// Push enqueues an interjection. Interrupt mode additionally wakes the
// running step (its context gets cancelled by the loop's watcher).
func (q *Interjector) Push(text string, mode InterjectionMode) Interjection {
	q.mu.Lock()
	q.seq++
	ij := Interjection{ID: "ij_" + itoa(q.seq), Text: text, Mode: mode}
	q.queue = append(q.queue, ij)
	q.mu.Unlock()
	if mode == ModeInterrupt {
		select {
		case q.notify <- struct{}{}:
		default:
		}
	}
	return ij
}

// DrainBoundary removes and returns all queued (non-interrupt) interjections
// in order. Called by the loop at delivery boundaries (SPEC §7.4).
func (q *Interjector) DrainBoundary() []Interjection {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []Interjection
	keep := q.queue[:0]
	for _, ij := range q.queue {
		if ij.Mode == ModeQueue {
			out = append(out, ij)
		} else {
			keep = append(keep, ij) // interrupts are taken via TakeInterrupts
		}
	}
	q.queue = keep
	return out
}

// TakeInterrupts removes and returns all interrupt-mode interjections.
func (q *Interjector) TakeInterrupts() []Interjection {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []Interjection
	keep := q.queue[:0]
	for _, ij := range q.queue {
		if ij.Mode == ModeInterrupt {
			out = append(out, ij)
		} else {
			keep = append(keep, ij)
		}
	}
	q.queue = keep
	// re-arm: if queue still holds interrupts, keep notify signalled
	for _, ij := range keep {
		if ij.Mode == ModeInterrupt {
			select {
			case q.notify <- struct{}{}:
			default:
			}
			break
		}
	}
	return out
}

// Pending lists everything still queued (for the CLI/§5.3 chip).
func (q *Interjector) Pending() []Interjection {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Interjection, len(q.queue))
	copy(out, q.queue)
	return out
}

// Withdraw removes a queued interjection by id (interrupts cannot be
// withdrawn once signalled; they are simply not taken).
func (q *Interjector) Withdraw(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, ij := range q.queue {
		if ij.ID == id {
			q.queue = append(q.queue[:i], q.queue[i+1:]...)
			return true
		}
	}
	return false
}

// Notify receives one signal per pending interrupt (level-ish: re-armed by
// TakeInterrupts while interrupts remain).
func (q *Interjector) Notify() <-chan struct{} { return q.notify }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// InterjectionPrefix is the non-skippable instruction prepended at delivery
// (SPEC §7.4: "the agent cannot skip it").
const InterjectionPrefix = "[user interjection — address this before continuing]\n"

// InterruptedResult is the synthetic tool result closing cancelled calls
// (SPEC §7.4).
const InterruptedResult = "[interrupted — user sent a new message]"
