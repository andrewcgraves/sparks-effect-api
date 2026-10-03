package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
)

type fakeAdminUserStore struct {
	summaries []account.UserSummary
	users     map[string]account.User
	patches   []account.UserPatch
}

func (f *fakeAdminUserStore) ListUsers(context.Context) ([]account.UserSummary, error) {
	return f.summaries, nil
}

func (f *fakeAdminUserStore) PatchUser(_ context.Context, id string, p account.UserPatch) (account.User, bool, error) {
	u, ok := f.users[id]
	if !ok {
		return account.User{}, false, nil
	}
	f.patches = append(f.patches, p)
	if p.IsAdmin != nil {
		u.IsAdmin = *p.IsAdmin
	}
	if p.Disabled != nil {
		u.DisabledAt = nil
		if *p.Disabled {
			at := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
			u.DisabledAt = &at
		}
	}
	f.users[id] = u
	return u, true, nil
}

var adminCaller = account.User{ID: "admin-1", Email: "admin@example.com", IsAdmin: true}

func adminRequest(t *testing.T, h http.Handler, method, path, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if id != "" {
		req.SetPathValue("id", id)
	}
	req = req.WithContext(auth.WithUser(req.Context(), adminCaller))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestListUsersReportsDisabledStateAndCountsForEveryAccount(t *testing.T) {
	disabled := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	store := &fakeAdminUserStore{summaries: []account.UserSummary{
		{ID: "admin-1", Email: "admin@example.com", IsAdmin: true,
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), ServiceCount: 3, PublishedCount: 1},
		{ID: "user-1", Email: "gone@example.com", Name: "Gone",
			CreatedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), DisabledAt: &disabled},
	}}

	rec := adminRequest(t, handler.ListUsers(store), http.MethodGet, "/api/admin/users", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	const want = `[` +
		`{"id":"admin-1","email":"admin@example.com","name":"","is_admin":true,"created_at":"2026-01-01T00:00:00Z","disabled_at":null,"service_count":3,"published_count":1},` +
		`{"id":"user-1","email":"gone@example.com","name":"Gone","is_admin":false,"created_at":"2026-02-01T00:00:00Z","disabled_at":"2026-09-01T12:00:00Z","service_count":0,"published_count":0}` +
		`]`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body:\n  got  %s\n  want %s", got, want)
	}
}

func TestListUsersWithNoAccountsIsAnEmptyArray(t *testing.T) {
	rec := adminRequest(t, handler.ListUsers(&fakeAdminUserStore{}), http.MethodGet, "/api/admin/users", "", "")
	if got := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusOK || got != "[]" {
		t.Errorf("status %d body %s, want 200 []", rec.Code, got)
	}
}

func TestPatchUserPromotesAndDisablesAnotherAccount(t *testing.T) {
	store := &fakeAdminUserStore{users: map[string]account.User{
		"user-1": {ID: "user-1", Email: "user@example.com"},
	}}

	rec := adminRequest(t, handler.PatchUser(store), http.MethodPatch, "/api/admin/users/user-1", "user-1",
		`{"is_admin":true,"disabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var got account.User
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != "user-1" || !got.IsAdmin || got.DisabledAt == nil {
		t.Errorf("patched user = %+v, want admin and disabled", got)
	}
	if len(store.patches) != 1 {
		t.Fatalf("patches = %d, want 1 applied together", len(store.patches))
	}
}

func TestPatchUserOmittedFieldsAreLeftAlone(t *testing.T) {
	store := &fakeAdminUserStore{users: map[string]account.User{
		"user-1": {ID: "user-1", Email: "user@example.com", IsAdmin: true},
	}}

	rec := adminRequest(t, handler.PatchUser(store), http.MethodPatch, "/api/admin/users/user-1", "user-1",
		`{"disabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if p := store.patches[0]; p.IsAdmin != nil || p.Disabled == nil || *p.Disabled {
		t.Errorf("patch = is_admin %v disabled %v, want only disabled=false", p.IsAdmin, p.Disabled)
	}
}

func TestPatchUserRefusesAnAdminDemotingOrDisablingThemselves(t *testing.T) {
	for _, body := range []string{
		`{"is_admin":false}`,
		`{"disabled":true}`,
		`{"is_admin":true,"disabled":true}`,
	} {
		t.Run(body, func(t *testing.T) {
			store := &fakeAdminUserStore{users: map[string]account.User{adminCaller.ID: adminCaller}}
			rec := adminRequest(t, handler.PatchUser(store), http.MethodPatch,
				"/api/admin/users/"+adminCaller.ID, adminCaller.ID, body)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body.String())
			}
			if len(store.patches) != 0 {
				t.Errorf("store was patched despite the refusal: %+v", store.patches)
			}
		})
	}
}

func TestPatchUserLetsAnAdminRestateTheirOwnAccess(t *testing.T) {
	store := &fakeAdminUserStore{users: map[string]account.User{adminCaller.ID: adminCaller}}
	rec := adminRequest(t, handler.PatchUser(store), http.MethodPatch,
		"/api/admin/users/"+adminCaller.ID, adminCaller.ID, `{"is_admin":true,"disabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
}

func TestPatchUserRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name, id, body string
		want           int
	}{
		{"unknown user", "nobody", `{"is_admin":true}`, http.StatusNotFound},
		{"nothing to change", "user-1", `{}`, http.StatusBadRequest},
		{"null fields", "user-1", `{"is_admin":null,"disabled":null}`, http.StatusBadRequest},
		{"malformed json", "user-1", `{"is_admin":`, http.StatusBadRequest},
		{"wrong type", "user-1", `{"disabled":"yes"}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeAdminUserStore{users: map[string]account.User{
				"user-1": {ID: "user-1", Email: "user@example.com"},
			}}
			rec := adminRequest(t, handler.PatchUser(store), http.MethodPatch, "/api/admin/users/"+tt.id, tt.id, tt.body)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d; body %s", rec.Code, tt.want, rec.Body.String())
			}
			if len(store.patches) != 0 {
				t.Errorf("store was patched: %+v", store.patches)
			}
		})
	}
}
