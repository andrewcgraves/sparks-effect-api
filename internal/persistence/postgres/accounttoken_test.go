package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

var testRepoHasher = auth.NewHasher(bcrypt.MinCost)

func mustNewToken(t *testing.T) (raw, hash string) {
	t.Helper()
	raw, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	return raw, hash
}

func TestInviteCreatesAPasswordlessUserAndALiveToken(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)

	raw, hash := mustNewToken(t)
	u := account.User{ID: ownerAID, Email: "invitee@example.com", Name: "Invitee"}
	expires := time.Now().Add(7 * 24 * time.Hour)
	if err := repo.CreateInvite(ctx, u, account.Token{
		TokenHash: hash, Purpose: account.TokenPurposeInvite, ExpiresAt: expires,
	}); err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}

	if _, _, found, err := repo.GetUserCredentialsByEmail(ctx, u.Email); err != nil || !found {
		t.Fatalf("invited user: found=%v err=%v", found, err)
	}
	if n := queryInt(t, url, `SELECT count(*) FROM users WHERE email = $1 AND password_hash = ''`, u.Email); n != 1 {
		t.Errorf("invited user should have no usable password; matching rows = %d", n)
	}

	tok, owner, found, err := repo.GetAccountToken(ctx, auth.HashToken(raw))
	if err != nil || !found {
		t.Fatalf("GetAccountToken: found=%v err=%v", found, err)
	}
	if tok.Purpose != account.TokenPurposeInvite || owner.Email != u.Email {
		t.Errorf("GetAccountToken = %+v, %+v", tok, owner)
	}
	if !tok.ExpiresAt.Round(time.Second).Equal(expires.Round(time.Second)) {
		t.Errorf("expires_at = %v, want %v", tok.ExpiresAt, expires)
	}

	if n := queryInt(t, url,
		`SELECT count(*) FROM account_tokens t WHERE strpos(t::text, $1) > 0`, raw); n != 0 {
		t.Errorf("raw token appears in %d account_tokens rows; only its hash may be stored", n)
	}
}

func TestRedeemingATokenSetsThePasswordAndReplacesSessions(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)

	u := account.User{ID: ownerAID, Email: "owner@example.com", Name: "Owner"}
	mustCreateUser(t, repo, u, "old-password")
	_, oldSession := mustNewToken(t)
	if err := repo.CreateSession(ctx, account.Session{
		TokenHash: oldSession, UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	raw, hash := mustNewToken(t)
	if err := repo.CreateResetToken(ctx, account.Token{
		TokenHash: hash, UserID: u.ID, Purpose: account.TokenPurposeReset, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateResetToken: %v", err)
	}

	newHash, err := testRepoHasher.Hash("brand-new-password")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	_, newSession := mustNewToken(t)
	got, ok, err := repo.RedeemAccountToken(ctx, auth.HashToken(raw), newHash,
		account.Session{TokenHash: newSession, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil || !ok {
		t.Fatalf("RedeemAccountToken: ok=%v err=%v", ok, err)
	}
	if got.ID != u.ID {
		t.Errorf("redeemed for %s, want %s", got.ID, u.ID)
	}

	_, stored, _, err := repo.GetUserCredentialsByEmail(ctx, u.Email)
	if err != nil {
		t.Fatalf("GetUserCredentialsByEmail: %v", err)
	}
	if !auth.VerifyPassword(stored, "brand-new-password") {
		t.Error("the new password does not verify")
	}
	if _, ok, _ := repo.GetSessionUser(ctx, oldSession); ok {
		t.Error("the earlier session survived redeeming the token")
	}
	if s, ok, _ := repo.GetSessionUser(ctx, newSession); !ok || s.ID != u.ID {
		t.Errorf("the new session does not resolve to the user: ok=%v user=%+v", ok, s)
	}

	if _, ok, err := repo.RedeemAccountToken(ctx, auth.HashToken(raw), newHash,
		account.Session{TokenHash: "another", ExpiresAt: time.Now().Add(time.Hour)}); ok || err != nil {
		t.Errorf("second redeem: ok=%v err=%v, want false/nil", ok, err)
	}
	if _, _, found, err := repo.GetAccountToken(ctx, hash); found || err != nil {
		t.Errorf("used token still readable: found=%v err=%v", found, err)
	}
	if n := queryInt(t, url, `SELECT count(*) FROM sessions WHERE token_hash = 'another'`); n != 0 {
		t.Error("a refused redeem still created a session")
	}
}

func TestANewResetTokenInvalidatesTheUnusedOne(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	u := account.User{ID: ownerAID, Email: "owner@example.com"}
	mustCreateUser(t, repo, u, "pw")

	_, first := mustNewToken(t)
	_, second := mustNewToken(t)
	for _, h := range []string{first, second} {
		if err := repo.CreateResetToken(ctx, account.Token{
			TokenHash: h, UserID: u.ID, Purpose: account.TokenPurposeReset, ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("CreateResetToken: %v", err)
		}
	}

	if _, _, found, err := repo.GetAccountToken(ctx, first); found || err != nil {
		t.Errorf("first reset token: found=%v err=%v, want false/nil", found, err)
	}
	if _, _, found, err := repo.GetAccountToken(ctx, second); !found || err != nil {
		t.Errorf("second reset token: found=%v err=%v, want true/nil", found, err)
	}
}

func TestExpiredAndDisabledTokensAreNotFound(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	u := account.User{ID: ownerAID, Email: "owner@example.com"}
	mustCreateUser(t, repo, u, "pw")
	other := account.User{ID: ownerBID, Email: "other@example.com"}
	mustCreateUser(t, repo, other, "pw")

	_, expired := mustNewToken(t)
	if err := repo.CreateResetToken(ctx, account.Token{
		TokenHash: expired, UserID: u.ID, Purpose: account.TokenPurposeReset, ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("CreateResetToken expired: %v", err)
	}
	_, disabled := mustNewToken(t)
	if err := repo.CreateResetToken(ctx, account.Token{
		TokenHash: disabled, UserID: other.ID, Purpose: account.TokenPurposeReset, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateResetToken disabled: %v", err)
	}
	if err := repo.SetUserDisabled(ctx, other.ID, true); err != nil {
		t.Fatalf("SetUserDisabled: %v", err)
	}

	for name, h := range map[string]string{"expired": expired, "disabled": disabled, "unknown": "nope"} {
		if _, _, found, err := repo.GetAccountToken(ctx, h); found || err != nil {
			t.Errorf("%s: GetAccountToken found=%v err=%v, want false/nil", name, found, err)
		}
		if _, ok, err := repo.RedeemAccountToken(ctx, h, "x",
			account.Session{TokenHash: "s-" + name, ExpiresAt: time.Now().Add(time.Hour)}); ok || err != nil {
			t.Errorf("%s: RedeemAccountToken ok=%v err=%v, want false/nil", name, ok, err)
		}
	}
}
