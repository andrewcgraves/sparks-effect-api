package auth_test

import (
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
)

func TestHashPasswordVerifies(t *testing.T) {
	hash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
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

// Equal passwords must produce different hashes — bcrypt salts per call, so a
// leaked table can't be attacked by grouping identical hashes.
func TestHashPasswordIsSalted(t *testing.T) {
	a, err := auth.HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	b, err := auth.HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
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

// VerifyNothing exists to make an unknown-account login cost the same as a
// wrong-password one. It must always fail, and must actually do the bcrypt
// work — a stub returning false immediately would reintroduce the timing leak.
func TestVerifyNothingAlwaysFailsAndCostsRealWork(t *testing.T) {
	for _, pw := range []string{"", "anything", "no account has this password"} {
		if auth.VerifyNothing(pw) {
			t.Errorf("VerifyNothing(%q) = true, must always be false", pw)
		}
	}

	// A real bcrypt comparison at the configured cost is far slower than a
	// bare return. This threshold is loose enough not to flake on slow CI but
	// tight enough to catch the work being skipped entirely.
	start := time.Now()
	auth.VerifyNothing("some-password")
	if elapsed := time.Since(start); elapsed < time.Millisecond {
		t.Errorf("VerifyNothing returned in %v — too fast to have hashed anything", elapsed)
	}
}

func TestHashPasswordRejectsEmptyPassword(t *testing.T) {
	if _, err := auth.HashPassword(""); err == nil {
		t.Error("HashPassword(\"\"): want error, got nil")
	}
}
