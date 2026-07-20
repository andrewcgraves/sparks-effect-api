package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// fakeAuthStore is an in-memory stand-in for the sessions/users tables.
type fakeAuthStore struct {
	users    map[string]userRecord // by email
	sessions map[string]transit.Session
	created  []transit.User
	failWith error
}

type userRecord struct {
	user transit.User
	hash string
}

func newFakeAuthStore(t *testing.T) *fakeAuthStore {
	t.Helper()
	hash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	adminHash, err := auth.HashPassword("admin-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	return &fakeAuthStore{
		users: map[string]userRecord{
			"user@example.com": {
				user: transit.User{ID: "user-1", Email: "user@example.com", Name: "User"},
				hash: hash,
			},
			"admin@example.com": {
				user: transit.User{ID: "admin-1", Email: "admin@example.com", IsAdmin: true},
				hash: adminHash,
			},
			// Provisioned but with no password set yet.
			"nopass@example.com": {
				user: transit.User{ID: "user-2", Email: "nopass@example.com"},
				hash: "",
			},
		},
		sessions: map[string]transit.Session{},
	}
}

func (f *fakeAuthStore) GetUserCredentialsByEmail(_ context.Context, email string) (transit.User, string, bool, error) {
	if f.failWith != nil {
		return transit.User{}, "", false, f.failWith
	}
	rec, ok := f.users[email]
	if !ok {
		return transit.User{}, "", false, nil
	}
	return rec.user, rec.hash, true, nil
}

func (f *fakeAuthStore) CreateSession(_ context.Context, s transit.Session) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.sessions[s.TokenHash] = s
	return nil
}

func (f *fakeAuthStore) DeleteSession(_ context.Context, tokenHash string) error {
	if f.failWith != nil {
		return f.failWith
	}
	delete(f.sessions, tokenHash)
	return nil
}

func (f *fakeAuthStore) GetUserByEmail(_ context.Context, email string) (transit.User, bool, error) {
	rec, ok := f.users[email]
	return rec.user, ok, nil
}

func (f *fakeAuthStore) CreateUser(_ context.Context, u transit.User, hash string) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.users[u.Email] = userRecord{user: u, hash: hash}
	f.created = append(f.created, u)
	return nil
}

func postJSON(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestLoginSuccessReturnsUsableToken(t *testing.T) {
	store := newFakeAuthStore(t)
	h := handler.Login(store, time.Hour)

	rec := postJSON(t, h, "/api/auth/login",
		`{"email":"user@example.com","password":"correct-password"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Token     string       `json:"token"`
		ExpiresAt time.Time    `json:"expires_at"`
		User      transit.User `json:"user"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("login returned an empty token")
	}
	if resp.User.ID != "user-1" {
		t.Errorf("user id = %q, want user-1", resp.User.ID)
	}
	if !resp.ExpiresAt.After(time.Now()) {
		t.Errorf("expires_at %v is not in the future", resp.ExpiresAt)
	}

	// The token must be persisted only as its hash, and be resolvable.
	if _, ok := store.sessions[auth.HashToken(resp.Token)]; !ok {
		t.Error("no session stored under the token's hash")
	}
	if _, ok := store.sessions[resp.Token]; ok {
		t.Error("the raw bearer token was stored — only its hash may be")
	}
}

// The response must never carry the password hash, even though the login path
// reads it.
func TestLoginResponseOmitsPasswordHash(t *testing.T) {
	store := newFakeAuthStore(t)
	rec := postJSON(t, handler.Login(store, time.Hour), "/api/auth/login",
		`{"email":"user@example.com","password":"correct-password"}`)

	body := rec.Body.String()
	if strings.Contains(body, "password_hash") || strings.Contains(body, "$2a$") {
		t.Errorf("login response leaked credential material: %s", body)
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"wrong password", `{"email":"user@example.com","password":"wrong"}`},
		{"unknown email", `{"email":"nobody@example.com","password":"correct-password"}`},
		{"empty password", `{"email":"user@example.com","password":""}`},
		// An account with no password set must not be loggable-into with "".
		{"account without a password", `{"email":"nopass@example.com","password":""}`},
		{"missing email", `{"password":"correct-password"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeAuthStore(t)
			rec := postJSON(t, handler.Login(store, time.Hour), "/api/auth/login", tt.body)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401; body %s", rec.Code, rec.Body.String())
			}
			if len(store.sessions) != 0 {
				t.Error("a session was created for a failed login")
			}
		})
	}
}

// A wrong password and an unknown email must be indistinguishable, or the
// endpoint becomes an account-enumeration oracle.
func TestLoginDoesNotRevealWhetherAccountExists(t *testing.T) {
	store := newFakeAuthStore(t)
	h := handler.Login(store, time.Hour)

	known := postJSON(t, h, "/api/auth/login", `{"email":"user@example.com","password":"wrong"}`)
	unknown := postJSON(t, h, "/api/auth/login", `{"email":"ghost@example.com","password":"wrong"}`)

	if known.Code != unknown.Code {
		t.Errorf("status differs: known account %d, unknown %d", known.Code, unknown.Code)
	}
	if known.Body.String() != unknown.Body.String() {
		t.Errorf("body differs:\n known: %s\n unknown: %s", known.Body, unknown.Body)
	}
}

func TestLoginRejectsMalformedJSON(t *testing.T) {
	store := newFakeAuthStore(t)
	rec := postJSON(t, handler.Login(store, time.Hour), "/api/auth/login", `{"email":`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// A store outage must be a 500, not a 401 that tells a valid user their
// credentials are wrong.
func TestLoginSurfacesStoreErrors(t *testing.T) {
	store := newFakeAuthStore(t)
	store.failWith = errors.New("db is down")
	rec := postJSON(t, handler.Login(store, time.Hour), "/api/auth/login",
		`{"email":"user@example.com","password":"correct-password"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestLogoutRevokesTheSession(t *testing.T) {
	store := newFakeAuthStore(t)
	token, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	store.sessions[hash] = transit.Session{
		TokenHash: hash, UserID: "user-1", ExpiresAt: time.Now().Add(time.Hour),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.Logout(store).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if _, ok := store.sessions[hash]; ok {
		t.Error("session survived logout")
	}
}

func TestMeReturnsTheAuthenticatedIdentity(t *testing.T) {
	user := transit.User{ID: "user-1", Email: "user@example.com", Name: "User"}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req = req.WithContext(auth.WithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	handler.Me().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got transit.User
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != user.ID || got.Email != user.Email {
		t.Errorf("got %+v, want %+v", got, user)
	}
}
