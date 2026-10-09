package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/fault"
)

type AuthStore interface {
	GetUserCredentialsByEmail(ctx context.Context, email string) (account.User, string, bool, error)
	CreateSession(ctx context.Context, s account.Session) error
	DeleteSession(ctx context.Context, tokenHash string) error
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token     string       `json:"token"`
	ExpiresAt time.Time    `json:"expires_at"`
	User      account.User `json:"user"`
}

const invalidCredentials = "invalid email or password"

const maxAuthBodyBytes = 4 << 10

func Login(store AuthStore, ttl time.Duration, hasher auth.Hasher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The body is capped before the decode: this route is public, and the
		// credential lookup and bcrypt comparison below are the expensive part.
		var req loginRequest
		if !decodeAuthBody(w, r, &req) {
			return
		}

		// Normalized identically to provisioning (CreateUser), so an account
		// created as "User@Example.com" can be logged into as typed.
		user, hash, found, err := store.GetUserCredentialsByEmail(r.Context(), normalizeEmail(req.Email))
		if err != nil {
			writeInternalError(r.Context(), w, "login credential lookup", err)
			return
		}
		// An unknown email still pays for a bcrypt comparison, so the two
		// failure modes cost the same time as well as returning the same body.
		// Skipping the work here would leak account existence through latency.
		if !found {
			hasher.VerifyNothing(req.Password)
			writeError(w, http.StatusUnauthorized, invalidCredentials)
			return
		}
		if !auth.VerifyPassword(hash, req.Password) {
			writeError(w, http.StatusUnauthorized, invalidCredentials)
			return
		}

		token, tokenHash, err := auth.NewToken()
		if err != nil {
			writeInternalError(r.Context(), w, "minting session token", err)
			return
		}

		expiresAt := time.Now().Add(ttl)
		if err := store.CreateSession(r.Context(), account.Session{
			TokenHash: tokenHash,
			UserID:    user.ID,
			ExpiresAt: expiresAt,
		}); err != nil {
			writeInternalError(r.Context(), w, "creating session", err)
			return
		}

		writeJSON(w, http.StatusOK, loginResponse{Token: token, ExpiresAt: expiresAt, User: user})
	}
}

func Logout(store AuthStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := auth.BearerToken(r)
		if !ok {
			// Unreachable behind RequireAuth; answered idempotently rather than
			// as an error, since "no session" is the state the caller wanted.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err := store.DeleteSession(r.Context(), auth.HashToken(token)); err != nil {
			writeInternalError(r.Context(), w, "deleting session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

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

type AccountStore interface {
	GetUserCredentialsByEmail(ctx context.Context, email string) (account.User, string, bool, error)
	UpdateUserName(ctx context.Context, id, name string) (account.User, bool, error)
	ChangePassword(ctx context.Context, c account.PasswordChange) (bool, error)
	DeleteUserSessions(ctx context.Context, userID string) error
}

const maxNameRunes = 80

// Only name is decoded, so is_admin, email or anything else in the body is
// ignored rather than applied.
type updateMeRequest struct {
	Name *string `json:"name"`
}

func UpdateMe(store AccountStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		var req updateMeRequest
		if !decodeAuthBody(w, r, &req) {
			return
		}

		name := ""
		if req.Name != nil {
			name = strings.TrimSpace(*req.Name)
		}
		switch {
		case name == "":
			writeUnprocessable(w, fault.ValidationFaults{
				fault.Whole("name", fault.RuleRequired, "name is required"),
			}.Err())
			return
		case utf8.RuneCountInString(name) > maxNameRunes:
			writeUnprocessable(w, fault.ValidationFaults{
				fault.Whole("name", fault.RuleMaxLength, fmt.Sprintf("name must be at most %d characters", maxNameRunes)),
			}.Err())
			return
		}

		updated, found, err := store.UpdateUserName(r.Context(), user.ID, name)
		if err != nil {
			writeInternalError(r.Context(), w, "updating user name", err)
			return
		}
		if !found {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func ChangePassword(store AccountStore, hasher auth.Hasher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		token, hasToken := auth.BearerToken(r)
		if !ok || !hasToken {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		var req changePasswordRequest
		if !decodeAuthBody(w, r, &req) {
			return
		}

		// Read by email through the login lookup so the comparison uses the
		// same stored hash and the same disabled filter login does.
		_, hash, found, err := store.GetUserCredentialsByEmail(r.Context(), user.Email)
		if err != nil {
			writeInternalError(r.Context(), w, "change-password credential lookup", err)
			return
		}
		if !found || !auth.VerifyPassword(hash, req.CurrentPassword) {
			writeError(w, http.StatusUnauthorized, "current password is incorrect")
			return
		}

		if err := auth.ValidatePassword(req.NewPassword, user.Email); err != nil {
			writeUnprocessable(w, renameFaultField(err, "password", "new_password"))
			return
		}

		newHash, err := hasher.Hash(req.NewPassword)
		if err != nil {
			writeInternalError(r.Context(), w, "hashing password", err)
			return
		}
		changed, err := store.ChangePassword(r.Context(), account.PasswordChange{
			UserID:        user.ID,
			CurrentHash:   hash,
			NewHash:       newHash,
			KeepTokenHash: auth.HashToken(token),
		})
		if err != nil {
			writeInternalError(r.Context(), w, "changing password", err)
			return
		}
		if !changed {
			// A concurrent change, a revoked session or a disable got there
			// first; none of those leaves this caller entitled to retry as-is.
			writeError(w, http.StatusConflict, "the account changed during the request; sign in again")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func RevokeAllSessions(store AccountStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if err := store.DeleteUserSessions(r.Context(), user.ID); err != nil {
			writeInternalError(r.Context(), w, "revoking sessions", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func decodeAuthBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		writeError(w, http.StatusBadRequest, "malformed request body")
		return false
	}
	return true
}

// ValidatePassword names its faults "password"; on this endpoint the client
// sent two passwords and needs to know which one was refused.
func renameFaultField(err error, from, to string) error {
	var faults fault.ValidationFaults
	if !errors.As(err, &faults) {
		return err
	}
	renamed := make(fault.ValidationFaults, len(faults))
	for i, f := range faults {
		if f.Field == from {
			f.Field = to
		}
		renamed[i] = f
	}
	return renamed.Err()
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
