// Package idgen provides cryptographically random ID generation.
package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// New generates a UUID-like random ID (32 hex chars with dashes).
// Format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
func New() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// WithPrefix generates a random ID with a prefix (e.g. "cmt_", "wh_", "pred_").
// Result is prefix + 24 hex chars (12 random bytes).
func WithPrefix(prefix string) string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return prefix + hex.EncodeToString(b)
}

// Hex generates a random hex string of the given byte length.
func Hex(numBytes int) string {
	b := make([]byte, numBytes)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
