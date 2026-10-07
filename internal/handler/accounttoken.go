package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
)

type AccountTokenStore interface {
	GetUserByEmail(ctx context.Context, email string) (account.User, bool, error)
	GetUserByID(ctx context.Context, id string) (account.User, bool, error)
	CreateInvite(ctx context.Context, u account.User, t account.Token) error
	CreateResetToken(ctx context.Context, t account.Token) error
	GetAccountToken(ctx context.Context, tokenHash string) (account.Token, account.User, bool, error)
	RedeemAccountToken(ctx context.Context, tokenHash, passwordHash string, s account.Session) (account.User, bool, error)
}

const (
	inviteTTL = 7 * 24 * time.Hour
	resetTTL  = time.Hour
)

// One body for unknown, used, expired, and disabled-account tokens, so the
// public endpoints say nothing about which it was.
const accountTokenNotFound = "link not found or no longer valid"

type inviteRequest struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	IsAdmin bool   `json:"is_admin"`
}

type inviteResponse struct {
	User account.User `json:"user"`
	URL  string       `json:"url"`
}

type linkResponse struct {
	URL string `json:"url"`
}

type accountTokenResponse struct {
	Purpose   account.TokenPurpose `json:"purpose"`
	Email     string               `json:"email"`
	ExpiresAt time.Time            `json:"expires_at"`
}

type redeemRequest struct {
	Password string `json:"password"`
}

func setPasswordLink(websiteURL, token string) string {
	return strings.TrimRight(websiteURL, "/") + "/set-password?token=" + url.QueryEscape(token)
}

func CreateInvite(store AccountTokenStore, websiteURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxCreateUserBodyBytes)

		var req inviteRequest
		if !decodeAccountBody(w, r, &req) {
			return
		}
		email := normalizeEmail(req.Email)
		if email == "" {
			writeError(w, http.StatusBadRequest, "email is required")
			return
		}

		// The same up-front 409 as CreateUser; the UNIQUE constraint on
		// users.email still decides a concurrent race.
		if _, exists, err := store.GetUserByEmail(r.Context(), email); err != nil {
			writeInternalError(r.Context(), w, "checking existing user", err)
			return
		} else if exists {
			writeError(w, http.StatusConflict, "an account with that email already exists")
			return
		}

		id, err := ids.NewUUID()
		if err != nil {
			writeInternalError(r.Context(), w, "generating user id", err)
			return
		}
		token, tokenHash, err := auth.NewToken()
		if err != nil {
			writeInternalError(r.Context(), w, "minting invite token", err)
			return
		}

		user := account.User{ID: id, Email: email, Name: strings.TrimSpace(req.Name), IsAdmin: req.IsAdmin}
		if err := store.CreateInvite(r.Context(), user, account.Token{
			TokenHash: tokenHash,
			UserID:    id,
			Purpose:   account.TokenPurposeInvite,
			ExpiresAt: time.Now().Add(inviteTTL),
		}); err != nil {
			writeInternalError(r.Context(), w, "creating invite", err)
			return
		}

		writeJSON(w, http.StatusCreated, inviteResponse{User: user, URL: setPasswordLink(websiteURL, token)})
	}
}

func CreateResetLink(store AccountTokenStore, websiteURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, found, err := store.GetUserByID(r.Context(), r.PathValue("id"))
		if err != nil {
			writeInternalError(r.Context(), w, "looking up user for reset link", err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}

		token, tokenHash, err := auth.NewToken()
		if err != nil {
			writeInternalError(r.Context(), w, "minting reset token", err)
			return
		}
		// Issued for a disabled account too: the link does nothing until the
		// account is re-enabled, and an admin may be preparing exactly that.
		if err := store.CreateResetToken(r.Context(), account.Token{
			TokenHash: tokenHash,
			UserID:    user.ID,
			Purpose:   account.TokenPurposeReset,
			ExpiresAt: time.Now().Add(resetTTL),
		}); err != nil {
			writeInternalError(r.Context(), w, "creating reset token", err)
			return
		}

		writeJSON(w, http.StatusCreated, linkResponse{URL: setPasswordLink(websiteURL, token)})
	}
}

func AccountToken(store AccountTokenStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, user, found, err := store.GetAccountToken(r.Context(), auth.HashToken(r.PathValue("token")))
		if err != nil {
			writeInternalError(r.Context(), w, "reading account token", err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, accountTokenNotFound)
			return
		}
		writeJSON(w, http.StatusOK, accountTokenResponse{Purpose: tok.Purpose, Email: user.Email, ExpiresAt: tok.ExpiresAt})
	}
}

func RedeemAccountToken(store AccountTokenStore, sessionTTL time.Duration, hasher auth.Hasher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Public, like login, and bcrypt is the expensive part of it.
		r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodyBytes)

		var req redeemRequest
		if !decodeAccountBody(w, r, &req) {
			return
		}

		tokenHash := auth.HashToken(r.PathValue("token"))
		// Read first only to learn the email the password policy checks
		// against. RedeemAccountToken re-checks the token as it claims it, so
		// a token used or disabled in between still answers 404.
		_, user, found, err := store.GetAccountToken(r.Context(), tokenHash)
		if err != nil {
			writeInternalError(r.Context(), w, "reading account token", err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, accountTokenNotFound)
			return
		}
		if err := auth.ValidatePassword(req.Password, user.Email); err != nil {
			writeUnprocessable(w, err)
			return
		}

		hash, err := hasher.Hash(req.Password)
		if err != nil {
			writeInternalError(r.Context(), w, "hashing password", err)
			return
		}
		session, sessionHash, err := auth.NewToken()
		if err != nil {
			writeInternalError(r.Context(), w, "minting session token", err)
			return
		}

		expiresAt := time.Now().Add(sessionTTL)
		user, redeemed, err := store.RedeemAccountToken(r.Context(), tokenHash, hash,
			account.Session{TokenHash: sessionHash, ExpiresAt: expiresAt})
		if err != nil {
			writeInternalError(r.Context(), w, "redeeming account token", err)
			return
		}
		if !redeemed {
			writeError(w, http.StatusNotFound, accountTokenNotFound)
			return
		}

		writeJSON(w, http.StatusOK, loginResponse{Token: session, ExpiresAt: expiresAt, User: user})
	}
}

func decodeAccountBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
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
