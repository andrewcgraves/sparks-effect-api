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
