package store

import (
	"crypto/rand"
	_ "embed"
	"fmt"
	"strings"
	"time"
)

// ULID-ish ids (SPEC §6.2): 10-byte big-endian ms timestamp + 6 random
// bytes, Crockford base32 → 26 chars, lexicographically sortable.

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewID returns a fresh sortable id.
func NewID() string { return NewIDAt(time.Now()) }

// NewIDAt is NewID with an explicit timestamp (tests).
func NewIDAt(t time.Time) string {
	var b [16]byte
	ms := uint64(t.UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	if _, err := rand.Read(b[6:]); err != nil {
		panic("store: rand: " + err.Error())
	}
	var out strings.Builder
	// 128 bits → 26 chars (last char carries 3 data bits + 2 pad bits).
	var n uint64
	bits := 0
	for _, c := range b {
		n = n<<8 | uint64(c)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out.WriteByte(crockford[(n>>bits)&31])
		}
	}
	out.WriteByte(crockford[(n<<(5-bits))&31]) // bits == 2
	return out.String()
}

// nowStamp is the canonical timestamp format (ISO-8601 UTC).
func nowStamp() string { return time.Now().UTC().Format(time.RFC3339) }

var _ = fmt.Sprintf
