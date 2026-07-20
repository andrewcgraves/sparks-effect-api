package auth

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// SessionLookup resolves a session token hash to the user it authenticates.
// It reports ok=false for a token that is unknown, revoked, or expired — the
// middleware treats all three identically, so a caller learns nothing about
// which case they hit.
//
// This is the seam between the middleware and the sessions table; Repo
// satisfies it via its GetSessionUser method.
type SessionLookup func(ctx context.Context, tokenHash string) (transit.User, bool, error)

type contextKey struct{}

var userKey contextKey

// WithUser returns a context carrying the authenticated identity.
func WithUser(ctx context.Context, u transit.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// UserFrom returns the authenticated identity placed on the context by
// RequireAuth. Handlers behind the middleware can rely on ok being true.
func UserFrom(ctx context.Context) (transit.User, bool) {
	u, ok := ctx.Value(userKey).(transit.User)
	return u, ok
}

// RequireAuth returns middleware that rejects any request without a valid
// bearer token and, on success, attaches the authenticated user to the request
// context for the wrapped handler.
func RequireAuth(lookup SessionLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				unauthorized(w)
				return
			}

			user, ok, err := lookup(r.Context(), HashToken(token))
			if err != nil {
				// A lookup failure is an outage, not a rejected credential.
				// Answering 401 here would tell a legitimate user their
				// session was invalid and send them to re-login pointlessly.
				log.Printf("auth: session lookup failed: %v", err)
				writeErr(w, http.StatusInternalServerError, "internal error")
				return
			}
			if !ok {
				unauthorized(w)
				return
			}

			next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
		})
	}
}

// RequireAdmin returns middleware that admits only authenticated admins. It is
// the gate for route-write and account-provisioning endpoints.
//
// An anonymous caller gets 401 and an authenticated non-admin gets 403, so
// clients can tell "log in" apart from "you may not do this".
func RequireAdmin(lookup SessionLookup) func(http.Handler) http.Handler {
	requireAuth := RequireAuth(lookup)
	return func(next http.Handler) http.Handler {
		return requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFrom(r.Context())
			if !ok || !user.IsAdmin {
				writeErr(w, http.StatusForbidden, "admin privileges required")
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}

// bearerToken extracts the token from an `Authorization: Bearer <token>`
// header. The scheme is matched case-insensitively per RFC 7235.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "bearer "
	header := r.Header.Get("Authorization")
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	return token, token != ""
}

func unauthorized(w http.ResponseWriter) {
	// Advertise the scheme so clients know how to authenticate (RFC 7235 §4.1).
	w.Header().Set("WWW-Authenticate", `Bearer realm="sparks-effect"`)
	writeErr(w, http.StatusUnauthorized, "authentication required")
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		log.Printf("auth: failed to write response: %v", err)
	}
}
