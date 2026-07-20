package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// stubLookup resolves exactly one token hash, standing in for the sessions
// table so the middleware can be tested without a database.
func stubLookup(wantHash string, u transit.User) auth.SessionLookup {
	return func(_ context.Context, hash string) (transit.User, bool, error) {
		if hash == wantHash {
			return u, true, nil
		}
		return transit.User{}, false, nil
	}
}

// echoUser reports the identity the middleware placed on the request context,
// so tests can assert the handler sees the right user — not merely a 200.
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
	user := transit.User{ID: "user-1", Email: "a@example.com"}
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
	lookup := stubLookup(auth.HashToken("good-token"), transit.User{ID: "user-1"})

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

// A failing session lookup must not be mistaken for "not authenticated" —
// a database outage should surface as 500, never silently as 401.
func TestRequireAuthSurfacesLookupErrors(t *testing.T) {
	lookup := func(context.Context, string) (transit.User, bool, error) {
		return transit.User{}, false, errors.New("db is down")
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
	admin := transit.User{ID: "admin-1", IsAdmin: true}
	h := auth.RequireAdmin(stubLookup(auth.HashToken("admin-token"), admin))(echoUser(t))

	req := httptest.NewRequest(http.MethodPost, "/api/admin/thing", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// The gate SPA-75's route-write endpoints hang off: an authenticated
// non-admin must get 403, not 200.
func TestRequireAdminForbidsNonAdmins(t *testing.T) {
	user := transit.User{ID: "user-1", IsAdmin: false}
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

// An unauthenticated caller on an admin route is 401 (who are you?), not 403
// (I know you and you may not) — the distinction matters to clients.
func TestRequireAdminRejectsAnonymous(t *testing.T) {
	h := auth.RequireAdmin(stubLookup(auth.HashToken("x"), transit.User{}))(
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
