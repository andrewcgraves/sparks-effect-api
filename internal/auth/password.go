// Package auth provides invite-only authentication and authorization for the
// API: password hashing, opaque session tokens, the middleware that protects
// mutating endpoints, and the server-side ownership predicate.
//
// Authentication is bearer-token based. A token is minted at login, stored only
// as a SHA-256 hash in the sessions table, and presented as
// `Authorization: Bearer <token>`. Tokens are opaque and DB-backed rather than
// JWTs so that logout genuinely revokes them and there is no signing key to
// manage or rotate.
package auth

import (
	"crypto/rand"
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// ErrEmptyPassword is returned by HashPassword when given a blank password.
// Refusing it at the hashing boundary means no code path can accidentally
// provision an account whose password is "".
var ErrEmptyPassword = errors.New("auth: password must not be empty")

// HashPassword returns a bcrypt hash suitable for storage in users.password_hash.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", ErrEmptyPassword
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// dummyHash is a valid bcrypt hash used by VerifyNothing to spend the same work
// as a real comparison.
//
// It hashes 32 random bytes generated at startup, not a fixed string: a literal
// would be a password that genuinely matches, so any future caller trusting the
// return value could be authenticated by submitting it. Random per-process
// content means no input can ever match.
//
// Generated at init rather than hard-coded also keeps it at the current cost
// setting — a stale constant at a lower cost would itself be a timing signal.
var dummyHash []byte

func init() {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic("auth: cannot generate dummy hash seed: " + err.Error())
	}
	h, err := bcrypt.GenerateFromPassword(secret, bcrypt.DefaultCost)
	if err != nil {
		// Only reachable if bcrypt itself is broken, in which case no
		// authentication is possible anyway.
		panic("auth: cannot generate dummy hash: " + err.Error())
	}
	dummyHash = h
}

// VerifyNothing performs a throwaway password comparison and always reports
// false.
//
// The login path calls it when no account matches the submitted email, so a
// request for an unknown address costs the same bcrypt work as one for a known
// address with the wrong password. Without it, response latency alone reveals
// which emails have accounts — the account-enumeration leak that returning an
// identical error message is meant to prevent.
//
// The comparison's result is deliberately discarded rather than returned: there
// is no account here to authenticate, so no input may ever produce true.
func VerifyNothing(password string) bool {
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
	return false
}

// VerifyPassword reports whether password matches the stored bcrypt hash.
//
// An empty hash never verifies. Users can exist before a password is set (the
// column defaults to ”), and bcrypt would reject such a hash as malformed
// anyway — but failing closed explicitly keeps that guarantee from resting on
// the library's error behaviour.
func VerifyPassword(hash, password string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
