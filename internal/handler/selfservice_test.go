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

func (f *fakeAuthStore) recordByID(id string) (string, userRecord, bool) {
	for email, rec := range f.users {
		if rec.user.ID == id {
			return email, rec, true
		}
	}
	return "", userRecord{}, false
}

func (f *fakeAuthStore) UpdateUserName(_ context.Context, id, name string) (account.User, bool, error) {
	if f.failWith != nil {
		return account.User{}, false, f.failWith
	}
	email, rec, ok := f.recordByID(id)
	if !ok {
		return account.User{}, false, nil
	}
	rec.user.Name = name
	f.users[email] = rec
	return rec.user, true, nil
}

func (f *fakeAuthStore) ChangePassword(_ context.Context, c account.PasswordChange) (bool, error) {
	if f.failWith != nil {
		return false, f.failWith
	}
	email, rec, ok := f.recordByID(c.UserID)
	if !ok || rec.hash != c.CurrentHash {
		return false, nil
	}
	if _, live := f.sessions[c.KeepTokenHash]; !live {
		return false, nil
	}
	rec.hash = c.NewHash
	f.users[email] = rec
	for h, s := range f.sessions {
		if s.UserID == c.UserID && h != c.KeepTokenHash {
			delete(f.sessions, h)
		}
	}
	return true, nil
}

func (f *fakeAuthStore) DeleteUserSessions(_ context.Context, userID string) error {
	if f.failWith != nil {
		return f.failWith
	}
	for h, s := range f.sessions {
		if s.UserID == userID {
			delete(f.sessions, h)
		}
	}
	return nil
}

func (f *fakeAuthStore) addSession(t *testing.T, userID string) (token, hash string) {
	t.Helper()
	token, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	f.sessions[hash] = account.Session{TokenHash: hash, UserID: userID, ExpiresAt: time.Now().Add(time.Hour)}
	return token, hash
}

func callAs(t *testing.T, h http.Handler, method, path, token string, user account.User, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(auth.WithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var signedInUser = account.User{ID: "user-1", Email: "user@example.com", Name: "User"}

const strongPassword = "a sturdy new passphrase"

func TestChangePasswordRejectsAWrongCurrentPassword(t *testing.T) {
	store := newFakeAuthStore(t)
	token, _ := store.addSession(t, signedInUser.ID)
	_, other := store.addSession(t, signedInUser.ID)

	rec := callAs(t, handler.ChangePassword(store, testHasher), http.MethodPost, "/api/auth/password", token, signedInUser,
		`{"current_password":"not-the-password","new_password":"`+strongPassword+`"}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %s", rec.Code, rec.Body.String())
	}
	if !auth.VerifyPassword(store.users[signedInUser.Email].hash, "correct-password") {
		t.Error("password changed despite a wrong current password")
	}
	if _, ok := store.sessions[other]; !ok {
		t.Error("a wrong current password revoked another session")
	}
}

func TestChangePasswordRejectsAWeakNewPassword(t *testing.T) {
	store := newFakeAuthStore(t)
	token, _ := store.addSession(t, signedInUser.ID)

	rec := callAs(t, handler.ChangePassword(store, testHasher), http.MethodPost, "/api/auth/password", token, signedInUser,
		`{"current_password":"correct-password","new_password":"short"}`)

	body := decodeValidationFault(t, rec)
	if body.Code != handler.ValidationErrorCode {
		t.Errorf("code = %q, want %q", body.Code, handler.ValidationErrorCode)
	}
	if len(body.Detail.Faults) == 0 || body.Detail.Faults[0].Field != "new_password" {
		t.Errorf("faults = %+v, want one naming new_password", body.Detail.Faults)
	}
	if !auth.VerifyPassword(store.users[signedInUser.Email].hash, "correct-password") {
		t.Error("a weak new password was stored")
	}
}

func TestChangePasswordStoresTheNewHashAndKeepsOnlyThePresentingSession(t *testing.T) {
	store := newFakeAuthStore(t)
	token, presenting := store.addSession(t, signedInUser.ID)
	_, second := store.addSession(t, signedInUser.ID)

	rec := callAs(t, handler.ChangePassword(store, testHasher), http.MethodPost, "/api/auth/password", token, signedInUser,
		`{"current_password":"correct-password","new_password":"`+strongPassword+`"}`)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if !auth.VerifyPassword(store.users[signedInUser.Email].hash, strongPassword) {
		t.Error("new password does not verify against the stored hash")
	}
	if _, ok := store.sessions[presenting]; !ok {
		t.Error("the presenting session was revoked")
	}
	if _, ok := store.sessions[second]; ok {
		t.Error("a second session survived the password change")
	}
}

func TestChangePasswordAnswersConflictWhenTheStoreRefuses(t *testing.T) {
	store := newFakeAuthStore(t)
	// A token with no live session behind it, as when a concurrent revoke-all
	// lands between authentication and the change.
	token, _, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	rec := callAs(t, handler.ChangePassword(store, testHasher), http.MethodPost, "/api/auth/password", token, signedInUser,
		`{"current_password":"correct-password","new_password":"`+strongPassword+`"}`)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	if !auth.VerifyPassword(store.users[signedInUser.Email].hash, "correct-password") {
		t.Error("password changed although the store refused")
	}
}

func TestChangePasswordRejectsMalformedJSON(t *testing.T) {
	store := newFakeAuthStore(t)
	token, _ := store.addSession(t, signedInUser.ID)

	rec := callAs(t, handler.ChangePassword(store, testHasher), http.MethodPost, "/api/auth/password", token, signedInUser, `{`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestRevokeAllSessionsLeavesTheUserNone(t *testing.T) {
	store := newFakeAuthStore(t)
	token, _ := store.addSession(t, signedInUser.ID)
	store.addSession(t, signedInUser.ID)
	_, admins := store.addSession(t, "admin-1")

	rec := callAs(t, handler.RevokeAllSessions(store), http.MethodPost, "/api/auth/sessions/revoke-all", token, signedInUser, "")

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	for _, s := range store.sessions {
		if s.UserID == signedInUser.ID {
			t.Fatalf("a session for %s survived revoke-all", signedInUser.ID)
		}
	}
	if _, ok := store.sessions[admins]; !ok {
		t.Error("revoke-all reached another user's session")
	}
}

func TestUpdateMeChangesOnlyTheName(t *testing.T) {
	store := newFakeAuthStore(t)

	rec := callAs(t, handler.UpdateMe(store), http.MethodPatch, "/api/auth/me", "tok", signedInUser,
		`{"name":"  Andrew  ","is_admin":true,"email":"evil@example.com"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var got account.User
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "Andrew" {
		t.Errorf("name = %q, want the trimmed %q", got.Name, "Andrew")
	}
	stored := store.users[signedInUser.Email].user
	if stored.Name != "Andrew" || stored.IsAdmin || got.IsAdmin || got.Email != signedInUser.Email {
		t.Errorf("PATCH /api/auth/me changed more than the name: stored %+v, returned %+v", stored, got)
	}
}

func TestUpdateMeBoundsTheName(t *testing.T) {
	cases := map[string]struct {
		body string
		rule string
	}{
		"missing":  {`{}`, "required"},
		"blank":    {`{"name":"   "}`, "required"},
		"too long": {`{"name":"` + strings.Repeat("é", 81) + `"}`, "max_length"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := newFakeAuthStore(t)
			rec := callAs(t, handler.UpdateMe(store), http.MethodPatch, "/api/auth/me", "tok", signedInUser, tc.body)
			body := decodeValidationFault(t, rec)
			if len(body.Detail.Faults) != 1 || body.Detail.Faults[0].Field != "name" || body.Detail.Faults[0].Rule != tc.rule {
				t.Errorf("faults = %+v, want one name/%s", body.Detail.Faults, tc.rule)
			}
			if store.users[signedInUser.Email].user.Name != "User" {
				t.Error("an invalid name was stored")
			}
		})
	}
}

func TestUpdateMeAcceptsEightyCharacters(t *testing.T) {
	store := newFakeAuthStore(t)
	name := strings.Repeat("é", 80)
	rec := callAs(t, handler.UpdateMe(store), http.MethodPatch, "/api/auth/me", "tok", signedInUser, `{"name":"`+name+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
}
