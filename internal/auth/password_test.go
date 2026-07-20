package auth_test

import (
	"strings"
	"testing"

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

func TestHashPasswordRejectsEmptyPassword(t *testing.T) {
	if _, err := auth.HashPassword(""); err == nil {
		t.Error("HashPassword(\"\"): want error, got nil")
	}
}
