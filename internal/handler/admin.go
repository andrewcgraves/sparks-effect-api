package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
)

type UserStore interface {
	CreateUser(ctx context.Context, u account.User, passwordHash string) error
	GetUserByEmail(ctx context.Context, email string) (account.User, bool, error)
}

type createUserRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	IsAdmin  bool   `json:"is_admin"`
}

func CreateUser(store UserStore, hasher auth.Hasher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}

		email := normalizeEmail(req.Email)
		if email == "" {
			writeError(w, http.StatusBadRequest, "email is required")
			return
		}
		if req.Password == "" {
			writeError(w, http.StatusBadRequest, "password is required")
			return
		}

		// Checked up front for a clean 409. The UNIQUE constraint on
		// users.email is still the authority under a concurrent create; this
		// only spares the common case an opaque database error.
		if _, exists, err := store.GetUserByEmail(r.Context(), email); err != nil {
			slog.ErrorContext(r.Context(), "handler: checking existing user failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		} else if exists {
			writeError(w, http.StatusConflict, "an account with that email already exists")
			return
		}

		hash, err := hasher.Hash(req.Password)
		if err != nil {
			slog.ErrorContext(r.Context(), "handler: hashing password failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		id, err := ids.NewUUID()
		if err != nil {
			slog.ErrorContext(r.Context(), "handler: generating user id failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		user := account.User{
			ID:      id,
			Email:   email,
			Name:    strings.TrimSpace(req.Name),
			IsAdmin: req.IsAdmin,
		}
		if err := store.CreateUser(r.Context(), user, hash); err != nil {
			slog.ErrorContext(r.Context(), "handler: creating user failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		writeJSON(w, http.StatusCreated, user)
	}
}
