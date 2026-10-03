package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

func linkToken(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link %q: %v", link, err)
	}
	tok := u.Query().Get("token")
	if tok == "" {
		t.Fatalf("link %q carries no token", link)
	}
	return tok
}

func redeem(t *testing.T, h http.Handler, tok, password string) (string, int) {
	t.Helper()
	rec := request(t, h, http.MethodPost, "/api/auth/tokens/"+tok, "", `{"password":"`+password+`"}`)
	if rec.Code != http.StatusOK {
		return "", rec.Code
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode redeem: %v", err)
	}
	return resp.Token, rec.Code
}

func TestIntegration_InviteThenResetLinkSetPasswordsWithoutAnAdminKnowingThem(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)

	rec := request(t, h, http.MethodPost, "/api/admin/invites", adminToken,
		`{"email":"invitee@example.com","name":"Invitee"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite: status %d, body %s", rec.Code, rec.Body.String())
	}
	var invited struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &invited); err != nil {
		t.Fatalf("decode invite: %v", err)
	}
	inviteTok := linkToken(t, invited.URL)

	if _, status := login(t, h, "invitee@example.com", ""); status != http.StatusUnauthorized {
		t.Fatalf("invited account signed in before setting a password: status %d", status)
	}
	if rec := request(t, h, http.MethodGet, "/api/auth/tokens/"+inviteTok, ""); rec.Code != http.StatusOK {
		t.Fatalf("GET invite token: status %d, body %s", rec.Code, rec.Body.String())
	}

	session, status := redeem(t, h, inviteTok, "invitee-chosen-password")
	if status != http.StatusOK {
		t.Fatalf("redeem invite: status %d", status)
	}
	if rec := request(t, h, http.MethodGet, "/api/auth/me", session); rec.Code != http.StatusOK {
		t.Fatalf("session from the invite does not work: status %d", rec.Code)
	}
	if _, status := redeem(t, h, inviteTok, "another-chosen-password"); status != http.StatusNotFound {
		t.Fatalf("second redeem of the invite: status %d, want 404", status)
	}

	rec = request(t, h, http.MethodPost, "/api/admin/users/"+invited.User.ID+"/reset-link", adminToken)
	if rec.Code != http.StatusCreated {
		t.Fatalf("reset-link: status %d, body %s", rec.Code, rec.Body.String())
	}
	var reset struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reset); err != nil {
		t.Fatalf("decode reset: %v", err)
	}

	if _, status := redeem(t, h, linkToken(t, reset.URL), "reset-chosen-password"); status != http.StatusOK {
		t.Fatalf("redeem reset: status %d", status)
	}
	if rec := request(t, h, http.MethodGet, "/api/auth/me", session); rec.Code != http.StatusUnauthorized {
		t.Errorf("the invite's session survived the reset: status %d, want 401", rec.Code)
	}
	if _, status := login(t, h, "invitee@example.com", "reset-chosen-password"); status != http.StatusOK {
		t.Errorf("login with the reset password: status %d", status)
	}
}

func TestIntegration_ResetLinkForADisabledAccountIsNotFound(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	provisionMember(t, h, adminToken, "member@example.com", "member-password")
	member, _, err := repo.GetUserByEmail(context.Background(), "member@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}

	rec := request(t, h, http.MethodPost, "/api/admin/users/"+member.ID+"/reset-link", adminToken)
	if rec.Code != http.StatusCreated {
		t.Fatalf("reset-link: status %d, body %s", rec.Code, rec.Body.String())
	}
	var reset struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reset); err != nil {
		t.Fatalf("decode reset: %v", err)
	}
	tok := linkToken(t, reset.URL)

	if err := repo.SetUserDisabled(context.Background(), member.ID, true); err != nil {
		t.Fatalf("SetUserDisabled: %v", err)
	}
	disabled := request(t, h, http.MethodGet, "/api/auth/tokens/"+tok, "")
	unknown := request(t, h, http.MethodGet, "/api/auth/tokens/not-a-token", "")
	if disabled.Code != http.StatusNotFound || disabled.Body.String() != unknown.Body.String() {
		t.Errorf("disabled account's link: status %d body %q, want the unknown-token 404 %q",
			disabled.Code, disabled.Body.String(), unknown.Body.String())
	}
	if _, status := redeem(t, h, tok, "a-strong-new-password"); status != http.StatusNotFound {
		t.Errorf("redeem for a disabled account: status %d, want 404", status)
	}
}
