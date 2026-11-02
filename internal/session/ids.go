package session

import (
	"crypto/rand"
	"encoding/hex"
)

// newID returns prefix and 128 random bits in hex. Session, seat, and token
// ids are not game randomness (invariant 3 covers dice only), so they come
// from crypto/rand directly. scripts/check-randomness.sh allows this file.
func newID(prefix string) string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // never fails (Go 1.24+)
	return prefix + hex.EncodeToString(b[:])
}
