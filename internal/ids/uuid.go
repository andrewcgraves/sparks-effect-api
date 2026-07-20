// Package ids generates the identifiers used for new domain rows.
//
// The schema uses native uuid primary keys, and seeded rows carry hand-written
// UUIDs. This package mints them for rows created at runtime without pulling in
// a dependency for what is a dozen lines over crypto/rand.
package ids

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// NewUUID returns a random (version 4, variant 1) UUID in canonical
// 8-4-4-4-12 hyphenated form, which is what Postgres's uuid type expects.
func NewUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("ids: generating uuid: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 1 (RFC 4122)

	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}
