package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestIntegration_AdminListsPromotesAndDisablesAccounts(t *testing.T) {
	h, repo := integrationServer(t)
	ctx := context.Background()
	adminToken := provisionAdminAndLogin(t, h, repo)
	memberToken := provisionMember(t, h, adminToken, "member@example.com", "member-password")

	if err := repo.CreateRoute(ctx, transit.Route{
		ID: mustUUID(t), Slug: "admin-users-route", Name: "Alignment", Mode: "rail", Bidirectional: true,
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122, 37}, {-121, 37}}},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	createUserServiceOverAPI(t, h, memberToken, "admin-users-route", "Member Line")

	for _, rec := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/admin/users", ""},
		{http.MethodPatch, "/api/admin/users/" + mustUUID(t), `{"is_admin":true}`},
	} {
		if got := request(t, h, rec.method, rec.path, memberToken, rec.body); got.Code != http.StatusForbidden {
			t.Fatalf("member %s %s: status %d, want 403", rec.method, rec.path, got.Code)
		}
	}

	rec := request(t, h, http.MethodGet, "/api/admin/users", adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d, body %s", rec.Code, rec.Body.String())
	}
	var listed []account.UserSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed) != 2 || listed[0].Email != "admin@example.com" || listed[1].Email != "member@example.com" {
		t.Fatalf("list = %+v, want admin then member", listed)
	}
	admin, member := listed[0], listed[1]
	if member.ServiceCount != 1 || member.PublishedCount != 0 || member.DisabledAt != nil || member.IsAdmin {
		t.Errorf("member row = %+v, want 1 service, 0 published, enabled, not admin", member)
	}

	for _, body := range []string{`{"is_admin":false}`, `{"disabled":true}`} {
		rec := request(t, h, http.MethodPatch, "/api/admin/users/"+admin.ID, adminToken, body)
		if rec.Code != http.StatusConflict {
			t.Fatalf("self %s: status %d, want 409; body %s", body, rec.Code, rec.Body.String())
		}
	}
	if rec := request(t, h, http.MethodGet, "/api/auth/me", adminToken); rec.Code != http.StatusOK {
		t.Fatalf("admin lost their session after a refused self-disable: status %d", rec.Code)
	}

	rec = request(t, h, http.MethodPatch, "/api/admin/users/"+member.ID, adminToken, `{"is_admin":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := request(t, h, http.MethodGet, "/api/admin/users", memberToken); rec.Code != http.StatusOK {
		t.Fatalf("promoted member listing users: status %d, want 200", rec.Code)
	}

	rec = request(t, h, http.MethodPatch, "/api/admin/users/"+member.ID, adminToken, `{"is_admin":false,"disabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("demote+disable: status %d, body %s", rec.Code, rec.Body.String())
	}
	var patched account.User
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.IsAdmin || patched.DisabledAt == nil {
		t.Fatalf("patched = %+v, want demoted and disabled", patched)
	}
	if rec := request(t, h, http.MethodGet, "/api/auth/me", memberToken); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled member's token on /api/auth/me: status %d, want 401", rec.Code)
	}

	rec = request(t, h, http.MethodPatch, "/api/admin/users/"+member.ID, adminToken, `{"disabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-enable: status %d, body %s", rec.Code, rec.Body.String())
	}
	if _, status := login(t, h, "member@example.com", "member-password"); status != http.StatusOK {
		t.Fatalf("login after re-enable: status %d, want 200", status)
	}

	if rec := request(t, h, http.MethodPatch, "/api/admin/users/"+mustUUID(t), adminToken, `{"is_admin":true}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown user: status %d, want 404", rec.Code)
	}
	if rec := request(t, h, http.MethodPatch, "/api/admin/users/not-a-uuid", adminToken, `{"disabled":true}`); rec.Code != http.StatusNotFound {
		t.Fatalf("malformed id: status %d, want 404", rec.Code)
	}
}
