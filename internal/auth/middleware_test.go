package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
)

func stubLookup(wantHash string, u account.User) auth.SessionLookup {
	return func(_ context.Context, hash string) (account.User, bool, error) {
		if hash == wantHash {
			return u, true, nil
		}
		return account.User{}, false, nil
	}
}

func echoUser(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := auth.UserFrom(r.Context())
		if !ok {
			t.Error("handler ran without an identity on the context")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(u.ID))
	}
}

func TestRequireAuthAcceptsValidBearerToken(t *testing.T) {
	user := account.User{ID: "user-1", Email: "a@example.com"}
	h := auth.RequireAuth(stubLookup(auth.HashToken("good-token"), user))(echoUser(t))

	req := httptest.NewRequest(http.MethodPost, "/api/thing", nil)
	req.Header.Set("Authorization", "Bearer good-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "user-1" {
		t.Errorf("handler saw identity %q, want user-1", rec.Body.String())
	}
}

func TestRequireAuthRejectsBadCredentials(t *testing.T) {
	lookup := stubLookup(auth.HashToken("good-token"), account.User{ID: "user-1"})

	tests := []struct {
		name   string
		header string
	}{
		{"no Authorization header", ""},
		{"unknown token", "Bearer nonexistent-token"},
		{"wrong scheme", "Basic good-token"},
		{"scheme with no token", "Bearer "},
		{"raw token without scheme", "good-token"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reached bool
			h := auth.RequireAuth(lookup)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				reached = true
			}))

			req := httptest.NewRequest(http.MethodPost, "/api/thing", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if reached {
				t.Error("protected handler ran despite failed authentication")
			}
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
		})
	}
}

func TestRequireAuthSurfacesLookupErrors(t *testing.T) {
	lookup := func(context.Context, string) (account.User, bool, error) {
		return account.User{}, false, errors.New("db is down")
	}
	var reached bool
	h := auth.RequireAuth(lookup)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/thing", nil)
	req.Header.Set("Authorization", "Bearer any-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if reached {
		t.Error("protected handler ran despite a lookup error")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestRequireAdminAllowsAdmins(t *testing.T) {
	admin := account.User{ID: "admin-1", IsAdmin: true}
	h := auth.RequireAdmin(stubLookup(auth.HashToken("admin-token"), admin))(echoUser(t))

	req := httptest.NewRequest(http.MethodPost, "/api/admin/thing", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestRequireAdminForbidsNonAdmins(t *testing.T) {
	user := account.User{ID: "user-1", IsAdmin: false}
	var reached bool
	h := auth.RequireAdmin(stubLookup(auth.HashToken("user-token"), user))(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	req := httptest.NewRequest(http.MethodPost, "/api/admin/thing", nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if reached {
		t.Error("admin-only handler ran for a non-admin")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestRequireAdminRejectsAnonymous(t *testing.T) {
	h := auth.RequireAdmin(stubLookup(auth.HashToken("x"), account.User{}))(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/admin/thing", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestUserFromEmptyContext(t *testing.T) {
	if _, ok := auth.UserFrom(context.Background()); ok {
		t.Error("UserFrom on a bare context must report no identity")
	}
}

func TestRequireWorkerTokenAcceptsTheSharedSecret(t *testing.T) {
	var reached bool
	h := auth.RequireWorkerToken("worker-secret")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/internal/worker", nil)
	req.Header.Set("Authorization", "Bearer worker-secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if !reached {
		t.Error("handler was not reached")
	}
}

func TestRequireWorkerTokenRejectsBadCredentials(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{"no Authorization header", ""},
		{"wrong token", "Bearer other-secret"},
		{"user-looking token", "Bearer good-token"},
		{"wrong scheme", "Basic worker-secret"},
		{"scheme with no token", "Bearer "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reached bool
			h := auth.RequireWorkerToken("worker-secret")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				reached = true
			}))
			req := httptest.NewRequest(http.MethodGet, "/api/internal/worker", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			if reached {
				t.Error("handler ran despite a rejected token")
			}
		})
	}
}

func TestRequireWorkerTokenRejectsWhenUnset(t *testing.T) {
	var reached bool
	h := auth.RequireWorkerToken("")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/internal/worker", nil)
	req.Header.Set("Authorization", "Bearer worker-secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when the expected token is empty", rec.Code)
	}
	if reached {
		t.Error("handler ran with no worker token configured")
	}
}
