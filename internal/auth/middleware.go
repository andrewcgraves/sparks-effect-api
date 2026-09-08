package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type SessionLookup func(ctx context.Context, tokenHash string) (transit.User, bool, error)

type contextKey struct{}

var userKey contextKey

func WithUser(ctx context.Context, u transit.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

func UserFrom(ctx context.Context) (transit.User, bool) {
	u, ok := ctx.Value(userKey).(transit.User)
	return u, ok
}

func RequireAuth(lookup SessionLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := BearerToken(r)
			if !ok {
				unauthorized(w)
				return
			}

			user, ok, err := lookup(r.Context(), HashToken(token))
			if err != nil {
				// A lookup failure is an outage, not a rejected credential.
				// Answering 401 here would tell a legitimate user their
				// session was invalid and send them to re-login pointlessly.
				slog.ErrorContext(r.Context(), "auth: session lookup failed", "error", err)
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

func OptionalAuth(lookup SessionLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := BearerToken(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			user, ok, err := lookup(r.Context(), HashToken(token))
			if err != nil {
				// An outage, not a rejected credential — and unlike RequireAuth
				// this endpoint can still serve its public half, so failing the
				// whole request would be worse than serving it anonymously.
				// The owned half answers 404 in that case, which is the same
				// thing it tells any non-owner.
				slog.ErrorContext(r.Context(), "auth: optional session lookup failed", "error", err)
				next.ServeHTTP(w, r)
				return
			}
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
		})
	}
}

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

func BearerToken(r *http.Request) (string, bool) {
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

func RequireWorkerToken(expected string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if expected == "" {
				writeErr(w, http.StatusServiceUnavailable, "worker API is not configured")
				return
			}
			token, ok := BearerToken(r)
			if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(expected)) != 1 {
				unauthorized(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		slog.Error("auth: failed to write response", "error", err)
	}
}
