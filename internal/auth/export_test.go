package auth

import "golang.org/x/crypto/bcrypt"

// Test-only surface. VerifyNothing's cost comes from the stand-in hash, not
// from Hasher.Cost, so the tests read that hash's cost directly. go doc of
// this package does not include this file.

func DummyCost(h Hasher) (int, error) {
	return bcrypt.Cost(h.dummy())
}
