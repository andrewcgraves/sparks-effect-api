package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
)

const newPassphrase = "a sturdy new passphrase"

func TestIntegration_ChangePasswordEndsOtherSessionsAndKeepsThePresentingOne(t *testing.T) {
	h, repo := integrationServer(t)
	provisionAdmin(t, repo, "admin@example.com", "admin-password")
	presenting, _ := login(t, h, "admin@example.com", "admin-password")
	second, _ := login(t, h, "admin@example.com", "admin-password")

	if rec := request(t, h, http.MethodPost, "/api/auth/password", presenting,
		`{"current_password":"wrong-password","new_password":"`+newPassphrase+`"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current password: status %d, want 401; body %s", rec.Code, rec.Body)
	}
	if rec := request(t, h, http.MethodPost, "/api/auth/password", presenting,
		`{"current_password":"admin-password","new_password":"password"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("weak new password: status %d, want 422; body %s", rec.Code, rec.Body)
	}

	rec := request(t, h, http.MethodPost, "/api/auth/password", presenting,
		`{"current_password":"admin-password","new_password":"`+newPassphrase+`"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("change password: status %d, want 204; body %s", rec.Code, rec.Body)
	}

	if rec := request(t, h, http.MethodGet, "/api/auth/me", second); rec.Code != http.StatusUnauthorized {
		t.Errorf("second session after password change: status %d, want 401", rec.Code)
	}
	if rec := request(t, h, http.MethodGet, "/api/auth/me", presenting); rec.Code != http.StatusOK {
		t.Errorf("presenting session after password change: status %d, want 200", rec.Code)
	}
	if _, status := login(t, h, "admin@example.com", "admin-password"); status != http.StatusUnauthorized {
		t.Errorf("old password still logs in: status %d, want 401", status)
	}
	if _, status := login(t, h, "admin@example.com", newPassphrase); status != http.StatusOK {
		t.Errorf("new password: status %d, want 200", status)
	}
}

func TestIntegration_RevokeAllEndsEverySessionIncludingTheCallers(t *testing.T) {
	h, repo := integrationServer(t)
	provisionAdmin(t, repo, "admin@example.com", "admin-password")
	caller, _ := login(t, h, "admin@example.com", "admin-password")
	other, _ := login(t, h, "admin@example.com", "admin-password")

	if rec := request(t, h, http.MethodPost, "/api/auth/sessions/revoke-all", caller); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke-all: status %d, want 204; body %s", rec.Code, rec.Body)
	}
	for name, token := range map[string]string{"caller": caller, "other": other} {
		if rec := request(t, h, http.MethodGet, "/api/auth/me", token); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s session after revoke-all: status %d, want 401", name, rec.Code)
		}
	}
}

func TestIntegration_UpdateMeChangesOnlyTheName(t *testing.T) {
	h, repo := integrationServer(t)
	provisionAdmin(t, repo, "admin@example.com", "admin-password")
	token, _ := login(t, h, "admin@example.com", "admin-password")

	rec := request(t, h, http.MethodPatch, "/api/auth/me", token,
		`{"name":" Andrew ","is_admin":false,"email":"someone-else@example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/auth/me: status %d, want 200; body %s", rec.Code, rec.Body)
	}

	rec = request(t, h, http.MethodGet, "/api/auth/me", token)
	var me account.User
	if err := json.NewDecoder(rec.Body).Decode(&me); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if me.Name != "Andrew" || me.Email != "admin@example.com" || !me.IsAdmin {
		t.Errorf("after PATCH, /api/auth/me = %+v; want only the name changed", me)
	}
}
