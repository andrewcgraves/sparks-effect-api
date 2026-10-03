package postgres_test

import (
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
)

func TestListUsersCountsAuthoredAndPublishedServicesInSignUpOrder(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)

	published := sampleUserService()
	if err := repo.CreateUserService(ctx, published); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}
	draft := sampleUserService()
	draft.ID = "00000000-0000-4008-8003-0000000000e2"
	draft.Slug = "draft-only"
	if err := repo.CreateUserService(ctx, draft); err != nil {
		t.Fatalf("CreateUserService draft: %v", err)
	}
	succeedUserServiceCompile(t, repo, ctx, published.ID, pubJobID, nil)
	if _, err := publishUserService(ctx, repo, published.ID); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := repo.SetUserDisabled(ctx, usStrangerID, true); err != nil {
		t.Fatalf("SetUserDisabled: %v", err)
	}

	got, err := repo.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListUsers = %d rows, want 2: %+v", len(got), got)
	}
	if got[0].ID != usOwnerID || got[1].ID != usStrangerID {
		t.Fatalf("order = %s, %s; want owner then stranger", got[0].ID, got[1].ID)
	}
	owner, stranger := got[0], got[1]
	if owner.ServiceCount != 2 || owner.PublishedCount != 1 {
		t.Errorf("owner counts = %d services, %d published; want 2, 1", owner.ServiceCount, owner.PublishedCount)
	}
	if owner.DisabledAt != nil {
		t.Errorf("owner disabled_at = %v, want nil", owner.DisabledAt)
	}
	if stranger.ServiceCount != 0 || stranger.PublishedCount != 0 {
		t.Errorf("stranger counts = %d, %d; want 0, 0", stranger.ServiceCount, stranger.PublishedCount)
	}
	if stranger.DisabledAt == nil {
		t.Error("stranger disabled_at = nil, want the disable instant")
	}
	if owner.Email != "owner@example.com" || owner.Name != "Owner" {
		t.Errorf("owner = %+v", owner)
	}
}

func TestListUsersOrdersByCreatedAtNotEmail(t *testing.T) {
	repo, url := freshRepo(t)
	ctx := t.Context()

	mustCreateUser(t, repo, account.User{ID: ownerAID, Email: "zed@example.com"}, "pw")
	mustCreateUser(t, repo, account.User{ID: ownerBID, Email: "amy@example.com"}, "pw")
	execSQL(t, url, `UPDATE users SET created_at = now() - interval '1 day' WHERE id = $1`, ownerAID)

	got, err := repo.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(got) != 2 || got[0].Email != "zed@example.com" || got[1].Email != "amy@example.com" {
		t.Fatalf("ListUsers = %+v, want zed then amy", got)
	}
}

func TestPatchUserPromotesDemotesAndDisables(t *testing.T) {
	repo, url := freshRepo(t)
	ctx := t.Context()

	u := account.User{ID: ownerAID, Email: "member@example.com", Name: "Member"}
	mustCreateUser(t, repo, u, "pw")

	yes, no := true, false
	got, found, err := repo.PatchUser(ctx, u.ID, account.UserPatch{IsAdmin: &yes})
	if err != nil || !found {
		t.Fatalf("PatchUser promote: found=%v err=%v", found, err)
	}
	if !got.IsAdmin || got.DisabledAt != nil {
		t.Fatalf("after promote = %+v, want admin and enabled", got)
	}

	_, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := repo.CreateSession(ctx, account.Session{
		TokenHash: hash, UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, found, err = repo.PatchUser(ctx, u.ID, account.UserPatch{IsAdmin: &no, Disabled: &yes})
	if err != nil || !found {
		t.Fatalf("PatchUser demote+disable: found=%v err=%v", found, err)
	}
	if got.IsAdmin || got.DisabledAt == nil {
		t.Fatalf("after demote+disable = %+v, want non-admin and disabled", got)
	}
	if n := queryInt(t, url, `SELECT count(*) FROM sessions WHERE user_id = $1`, u.ID); n != 0 {
		t.Errorf("sessions after disable = %d, want 0", n)
	}

	got, found, err = repo.PatchUser(ctx, u.ID, account.UserPatch{Disabled: &no})
	if err != nil || !found || got.DisabledAt != nil {
		t.Fatalf("PatchUser re-enable: found=%v err=%v user=%+v", found, err, got)
	}

	if _, found, err := repo.PatchUser(ctx, "00000000-0000-4009-8003-0000000000ff", account.UserPatch{IsAdmin: &yes}); err != nil || found {
		t.Fatalf("PatchUser unknown id: found=%v err=%v, want not found", found, err)
	}
	if _, found, err := repo.PatchUser(ctx, "not-a-uuid", account.UserPatch{Disabled: &yes}); err != nil || found {
		t.Fatalf("PatchUser malformed id: found=%v err=%v, want not found", found, err)
	}
}
