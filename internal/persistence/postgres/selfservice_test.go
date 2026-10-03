package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

func mustCreateSession(t *testing.T, repo interface {
	CreateSession(context.Context, account.Session) error
}, userID string) string {
	t.Helper()
	_, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := repo.CreateSession(context.Background(), account.Session{
		TokenHash: hash, UserID: userID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return hash
}

func TestChangePasswordKeepsOnlyThePresentingSession(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	u := account.User{ID: ownerAID, Email: "owner@example.com"}
	other := account.User{ID: ownerBID, Email: "other@example.com"}
	mustCreateUser(t, repo, u, "old-password")
	mustCreateUser(t, repo, other, "pw")

	presenting := mustCreateSession(t, repo, u.ID)
	second := mustCreateSession(t, repo, u.ID)
	othersSession := mustCreateSession(t, repo, other.ID)

	ok, err := repo.ChangePassword(ctx, account.PasswordChange{
		UserID: u.ID, CurrentHash: storedHash(t, repo, u.Email),
		NewHash: mustHash(t, "brand-new-password"), KeepTokenHash: presenting,
	})
	if err != nil || !ok {
		t.Fatalf("ChangePassword: ok=%v err=%v", ok, err)
	}

	_, hash, _, err := repo.GetUserCredentialsByEmail(ctx, u.Email)
	if err != nil {
		t.Fatalf("GetUserCredentialsByEmail: %v", err)
	}
	if !auth.VerifyPassword(hash, "brand-new-password") || auth.VerifyPassword(hash, "old-password") {
		t.Error("stored hash was not replaced with the new password's")
	}
	if _, ok, err := repo.GetSessionUser(ctx, presenting); !ok || err != nil {
		t.Errorf("presenting session: ok=%v err=%v, want it kept", ok, err)
	}
	if _, ok, err := repo.GetSessionUser(ctx, second); ok || err != nil {
		t.Errorf("second session: ok=%v err=%v, want it revoked", ok, err)
	}
	if _, ok, err := repo.GetSessionUser(ctx, othersSession); !ok || err != nil {
		t.Errorf("another user's session: ok=%v err=%v, want it untouched", ok, err)
	}
}

func mustHash(t *testing.T, password string) string {
	t.Helper()
	h, err := auth.NewHasher(bcrypt.MinCost).Hash(password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	return h
}

func storedHash(t *testing.T, repo interface {
	GetUserCredentialsByEmail(context.Context, string) (account.User, string, bool, error)
}, email string) string {
	t.Helper()
	_, h, ok, err := repo.GetUserCredentialsByEmail(context.Background(), email)
	if err != nil || !ok {
		t.Fatalf("GetUserCredentialsByEmail: ok=%v err=%v", ok, err)
	}
	return h
}

func TestChangePasswordRefusesWhenItsPreconditionsNoLongerHold(t *testing.T) {
	ctx := context.Background()

	t.Run("stored hash moved on", func(t *testing.T) {
		repo, _ := freshRepo(t)
		u := account.User{ID: ownerAID, Email: "owner@example.com"}
		mustCreateUser(t, repo, u, "old-password")
		other := mustCreateSession(t, repo, u.ID)
		stale := storedHash(t, repo, u.Email)

		if ok, err := repo.ChangePassword(ctx, account.PasswordChange{
			UserID: u.ID, CurrentHash: stale, NewHash: mustHash(t, "first-new-password"), KeepTokenHash: other,
		}); !ok || err != nil {
			t.Fatalf("first change: ok=%v err=%v", ok, err)
		}
		// A fresh live session, so only the stale hash is at fault.
		keep := mustCreateSession(t, repo, u.ID)
		if ok, err := repo.ChangePassword(ctx, account.PasswordChange{
			UserID: u.ID, CurrentHash: stale, NewHash: mustHash(t, "second-new-password"), KeepTokenHash: keep,
		}); ok || err != nil {
			t.Errorf("change against a stale hash: ok=%v err=%v, want false/nil", ok, err)
		}
		if !auth.VerifyPassword(storedHash(t, repo, u.Email), "first-new-password") {
			t.Error("a change against a stale hash overwrote the newer password")
		}
	})

	t.Run("presenting session revoked", func(t *testing.T) {
		repo, _ := freshRepo(t)
		u := account.User{ID: ownerAID, Email: "owner@example.com"}
		mustCreateUser(t, repo, u, "old-password")
		gone := mustCreateSession(t, repo, u.ID)
		other := mustCreateSession(t, repo, u.ID)
		if err := repo.DeleteSession(ctx, gone); err != nil {
			t.Fatalf("DeleteSession: %v", err)
		}

		if ok, err := repo.ChangePassword(ctx, account.PasswordChange{
			UserID: u.ID, CurrentHash: storedHash(t, repo, u.Email), NewHash: mustHash(t, "brand-new-password"), KeepTokenHash: gone,
		}); ok || err != nil {
			t.Errorf("revoked presenting session: ok=%v err=%v, want false/nil", ok, err)
		}
		if _, ok, _ := repo.GetSessionUser(ctx, other); !ok {
			t.Error("a refused change still revoked another session")
		}
	})

	t.Run("disabled or missing user", func(t *testing.T) {
		repo, _ := freshRepo(t)
		u := account.User{ID: ownerAID, Email: "owner@example.com"}
		mustCreateUser(t, repo, u, "old-password")
		hash := storedHash(t, repo, u.Email)
		keep := mustCreateSession(t, repo, u.ID)
		if err := repo.SetUserDisabled(ctx, u.ID, true); err != nil {
			t.Fatalf("SetUserDisabled: %v", err)
		}

		if ok, err := repo.ChangePassword(ctx, account.PasswordChange{
			UserID: u.ID, CurrentHash: hash, NewHash: "x", KeepTokenHash: keep,
		}); ok || err != nil {
			t.Errorf("disabled user: ok=%v err=%v, want false/nil", ok, err)
		}
		if ok, err := repo.ChangePassword(ctx, account.PasswordChange{
			UserID: ownerBID, CurrentHash: hash, NewHash: "x", KeepTokenHash: keep,
		}); ok || err != nil {
			t.Errorf("missing user: ok=%v err=%v, want false/nil", ok, err)
		}
	})
}

func TestDeleteUserSessionsRevokesEveryOneOfTheirs(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	u := account.User{ID: ownerAID, Email: "owner@example.com"}
	other := account.User{ID: ownerBID, Email: "other@example.com"}
	mustCreateUser(t, repo, u, "pw")
	mustCreateUser(t, repo, other, "pw")
	first := mustCreateSession(t, repo, u.ID)
	second := mustCreateSession(t, repo, u.ID)
	othersSession := mustCreateSession(t, repo, other.ID)

	if err := repo.DeleteUserSessions(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUserSessions: %v", err)
	}
	for _, h := range []string{first, second} {
		if _, ok, err := repo.GetSessionUser(ctx, h); ok || err != nil {
			t.Errorf("session survived revoke-all: ok=%v err=%v", ok, err)
		}
	}
	if _, ok, err := repo.GetSessionUser(ctx, othersSession); !ok || err != nil {
		t.Errorf("another user's session: ok=%v err=%v, want it untouched", ok, err)
	}
}

func TestUpdateUserNameChangesOnlyTheName(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)

	u := account.User{ID: ownerAID, Email: "owner@example.com", Name: "Bootstrap Admin", IsAdmin: true}
	mustCreateUser(t, repo, u, "pw")
	execSQL(t, url, `UPDATE users SET updated_at = now() - interval '1 hour' WHERE id = $1`, u.ID)
	before, _, err := repo.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}

	got, ok, err := repo.UpdateUserName(ctx, u.ID, "Andrew")
	if err != nil || !ok {
		t.Fatalf("UpdateUserName: ok=%v err=%v", ok, err)
	}
	if got.Name != "Andrew" {
		t.Errorf("name = %q, want Andrew", got.Name)
	}
	if got.Email != u.Email || !got.IsAdmin || !got.CreatedAt.Equal(before.CreatedAt) {
		t.Errorf("UpdateUserName touched more than the name: %+v", got)
	}
	if !got.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("updated_at = %v, want later than %v", got.UpdatedAt, before.UpdatedAt)
	}

	if _, ok, err := repo.UpdateUserName(ctx, ownerBID, "Nobody"); ok || err != nil {
		t.Errorf("missing user: ok=%v err=%v, want false/nil", ok, err)
	}
}
