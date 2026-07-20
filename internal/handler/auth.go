package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// AuthStore is the slice of the repository the auth endpoints need.
type AuthStore interface {
	GetUserCredentialsByEmail(ctx context.Context, email string) (transit.User, string, bool, error)
	CreateSession(ctx context.Context, s transit.Session) error
	DeleteSession(ctx context.Context, tokenHash string) error
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token     string       `json:"token"`
	ExpiresAt time.Time    `json:"expires_at"`
	User      transit.User `json:"user"`
}

// invalidCredentials is the single response for every authentication failure —
// unknown email, wrong password, or an account with no password set. Varying
// the message would turn the endpoint into an account-enumeration oracle.
const invalidCredentials = "invalid email or password"

// Login authenticates an admin-provisioned account and mints a session token
// valid for ttl. The token is returned once, here; only its hash is stored.
//
// There is deliberately no counterpart registration handler: accounts exist
// only via the admin-gated CreateUser endpoint.
func Login(store AuthStore, ttl time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}

		// Normalized identically to provisioning (CreateUser), so an account
		// created as "User@Example.com" can be logged into as typed.
		user, hash, found, err := store.GetUserCredentialsByEmail(r.Context(), normalizeEmail(req.Email))
		if err != nil {
			log.Printf("handler: login credential lookup failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		// Both branches answer identically; the lookup result only decides
		// whether there is a hash to check at all.
		if !found || !auth.VerifyPassword(hash, req.Password) {
			writeError(w, http.StatusUnauthorized, invalidCredentials)
			return
		}

		token, tokenHash, err := auth.NewToken()
		if err != nil {
			log.Printf("handler: minting session token failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		expiresAt := time.Now().Add(ttl)
		if err := store.CreateSession(r.Context(), transit.Session{
			TokenHash: tokenHash,
			UserID:    user.ID,
			ExpiresAt: expiresAt,
		}); err != nil {
			log.Printf("handler: creating session failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		writeJSON(w, http.StatusOK, loginResponse{Token: token, ExpiresAt: expiresAt, User: user})
	}
}

// Logout revokes the session behind the presented bearer token. It sits behind
// RequireAuth, so an unauthenticated caller never reaches it.
func Logout(store AuthStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			// Unreachable behind RequireAuth; answered idempotently rather than
			// as an error, since "no session" is the state the caller wanted.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err := store.DeleteSession(r.Context(), auth.HashToken(token)); err != nil {
			log.Printf("handler: deleting session failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// Me returns the authenticated identity, letting a client confirm a stored
// token is still valid and learn whether it carries admin rights.
func Me() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		writeJSON(w, http.StatusOK, user)
	}
}

// normalizeEmail is the single spelling of an address used as a lookup key, so
// provisioning and login can never disagree about which row an email names.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// bearerToken mirrors the middleware's header parsing for the one handler that
// needs the raw token rather than the resolved identity.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "bearer "
	header := r.Header.Get("Authorization")
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	return token, token != ""
}
