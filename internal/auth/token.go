package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// tokenBytes is the entropy behind a session token. 32 bytes (256 bits) puts
// brute-force guessing out of reach.
const tokenBytes = 32

// NewToken mints a session token, returning the bearer token to hand to the
// client and the hash to persist.
//
// Only the hash is ever stored: a leaked sessions table therefore yields no
// usable credentials. The token itself is returned exactly once, at login.
func NewToken() (token, hash string, err error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("auth: generating session token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	return token, HashToken(token), nil
}

// HashToken derives the stored form of a session token, so a token presented on
// a request can be looked up against the sessions table.
//
// SHA-256 rather than bcrypt is deliberate and safe here: unlike a
// user-chosen password, a token already carries 256 bits of entropy, so it is
// not vulnerable to the offline guessing that slow hashing defends against —
// and lookup happens on every authenticated request, where bcrypt's cost would
// be a per-request tax.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
