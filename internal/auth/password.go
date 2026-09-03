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
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// ErrEmptyPassword is returned by Hasher.Hash when given a blank password.
// Refusing it at the hashing boundary means no code path can accidentally
// provision an account whose password is "".
var ErrEmptyPassword = errors.New("auth: password must not be empty")

// Hasher hashes passwords at a fixed bcrypt cost.
//
// The cost is a property of the Hasher rather than a package constant so that
// tests can hash at bcrypt.MinCost. Password hashing is deliberately slow —
// that is the entire point of bcrypt — and a test suite that provisions and
// logs in dozens of accounts pays that cost dozens of times for no security
// benefit, because nothing it hashes ever leaves the throwaway database. At
// DefaultCost a single hash-or-compare costs ~46ms; at MinCost it costs ~1ms.
//
// Hasher is a value type and safe to copy: the lazily-built dummy hash is
// shared through the closure below, so copies of a Hasher share one dummy
// rather than each generating another.
type Hasher struct {
	cost int
	// dummy returns the VerifyNothing hash, generated at most once. It is nil
	// on the zero Hasher, which the methods treat as DefaultHasher.
	dummy func() []byte
}

// DefaultHasher hashes at bcrypt.DefaultCost. This is what production uses; the
// zero Hasher also falls back to it, so a Hasher that was never initialised
// fails towards the stronger cost rather than a weaker one.
var DefaultHasher = NewHasher(bcrypt.DefaultCost)

// NewHasher returns a Hasher at the given bcrypt cost. A cost outside bcrypt's
// accepted range — including the zero value — falls back to bcrypt.DefaultCost,
// the same soft-default pattern the config package uses for its env knobs, and
// again in the safe direction: a nonsense setting can only make hashing
// stronger, never weaker.
//
// The dummy hash VerifyNothing spends its time on is built on first use rather
// than at construction. Building it eagerly meant every binary that so much as
// linked this package paid for a full bcrypt hash at startup, including the
// test binaries of packages that never authenticate anything.
func NewHasher(cost int) Hasher {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		cost = bcrypt.DefaultCost
	}
	return Hasher{
		cost:  cost,
		dummy: sync.OnceValue(func() []byte { return newDummyHash(cost) }),
	}
}

// Hash returns a bcrypt hash suitable for storage in users.password_hash.
func (h Hasher) Hash(password string) (string, error) {
	if password == "" {
		return "", ErrEmptyPassword
	}
	b, err := bcrypt.GenerateFromPassword([]byte(password), h.effectiveCost())
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Cost reports the bcrypt cost this Hasher hashes at.
func (h Hasher) Cost() int { return h.effectiveCost() }

func (h Hasher) effectiveCost() int {
	if h.cost == 0 {
		return bcrypt.DefaultCost
	}
	return h.cost
}

// newDummyHash builds a valid bcrypt hash for VerifyNothing to spend the same
// work as a real comparison.
//
// It hashes 32 random bytes generated in-process, not a fixed string: a literal
// would be a password that genuinely matches, so any future caller trusting the
// return value could be authenticated by submitting it. Random per-process
// content means no input can ever match.
//
// Generating it rather than hard-coding it also keeps it at the Hasher's cost —
// a stale constant at a lower cost would itself be a timing signal.
func newDummyHash(cost int) []byte {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic("auth: cannot generate dummy hash seed: " + err.Error())
	}
	h, err := bcrypt.GenerateFromPassword(secret, cost)
	if err != nil {
		// Only reachable if bcrypt itself is broken, in which case no
		// authentication is possible anyway. The cost is already validated by
		// NewHasher, so it cannot be the cause.
		panic("auth: cannot generate dummy hash: " + err.Error())
	}
	return h
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
func (h Hasher) VerifyNothing(password string) bool {
	dummy := h.dummy
	if dummy == nil {
		dummy = DefaultHasher.dummy
	}
	_ = bcrypt.CompareHashAndPassword(dummy(), []byte(password))
	return false
}

// VerifyPassword reports whether password matches the stored bcrypt hash.
//
// This is not a Hasher method: the cost is recorded in the hash itself, so
// verification reads it from there and a stored hash keeps verifying correctly
// after the hashing cost is changed.
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
