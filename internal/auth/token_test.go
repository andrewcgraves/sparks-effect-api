package auth_test

import (
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
)

func TestNewTokenIsUniqueAndHashed(t *testing.T) {
	tok1, hash1, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	tok2, hash2, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	if tok1 == tok2 {
		t.Error("NewToken must not repeat tokens")
	}
	if hash1 == hash2 {
		t.Error("NewToken must not repeat hashes")
	}
	if tok1 == hash1 {
		t.Error("the stored hash must differ from the bearer token")
	}
	if len(tok1) < 32 {
		t.Errorf("token too short to resist guessing: %d chars", len(tok1))
	}
}

// The hash stored in the sessions table must be derivable from a presented
// token, or lookup on the auth path is impossible.
func TestHashTokenIsDeterministic(t *testing.T) {
	tok, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if got := auth.HashToken(tok); got != hash {
		t.Errorf("HashToken(token) = %q, want the hash NewToken returned (%q)", got, hash)
	}
	if auth.HashToken("some-other-token") == hash {
		t.Error("different tokens must hash differently")
	}
}
