package auth_test

import (
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

// MinCost still runs bcrypt. These tests cover salting and verification;
// the production hasher stays at bcrypt.DefaultCost.
var testHasher = auth.NewHasher(bcrypt.MinCost)

func TestHashPasswordVerifies(t *testing.T) {
	hash, err := testHasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if strings.Contains(hash, "correct horse") {
		t.Fatal("hash must not contain the plaintext password")
	}
	if !auth.VerifyPassword(hash, "correct horse battery staple") {
		t.Error("VerifyPassword: correct password rejected")
	}
	if auth.VerifyPassword(hash, "wrong password") {
		t.Error("VerifyPassword: wrong password accepted")
	}
}

func TestHashPasswordIsSalted(t *testing.T) {
	a, err := testHasher.Hash("same-password")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	b, err := testHasher.Hash("same-password")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if a == b {
		t.Error("two hashes of the same password must differ (missing salt)")
	}
}

func TestVerifyPasswordRejectsEmptyHash(t *testing.T) {
	// Users provisioned before a password is set carry an empty hash. That must
	// never authenticate, least of all against an empty password.
	if auth.VerifyPassword("", "") {
		t.Error("empty hash must not verify against empty password")
	}
	if auth.VerifyPassword("", "anything") {
		t.Error("empty hash must not verify")
	}
}

func TestVerifyNothingAlwaysFailsAndCostsRealWork(t *testing.T) {
	for _, pw := range []string{"", "anything", "no account has this password"} {
		if testHasher.VerifyNothing(pw) {
			t.Errorf("VerifyNothing(%q) = true, must always be false", pw)
		}
	}

	// A real bcrypt comparison is far slower than a bare return. MinCost on a
	// fast machine finishes in under a millisecond, so the bound sits below
	// that and well above an immediate return. It is there to catch the hash
	// being skipped entirely, not to pin a work factor.
	start := time.Now()
	testHasher.VerifyNothing("some-password")
	if elapsed := time.Since(start); elapsed < 100*time.Microsecond {
		t.Errorf("VerifyNothing returned in %v — too fast to have hashed anything", elapsed)
	}
}

func TestHashPasswordRejectsEmptyPassword(t *testing.T) {
	if _, err := testHasher.Hash(""); err == nil {
		t.Error("Hash(\"\"): want error, got nil")
	}
}
