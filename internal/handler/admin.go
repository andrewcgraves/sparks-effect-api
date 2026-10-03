package handler

import (
	"context"
	"encoding/json"
	"errors"
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

const maxCreateUserBodyBytes = 64 << 10

func CreateUser(store UserStore, hasher auth.Hasher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxCreateUserBodyBytes)

		var req createUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
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
		if err := auth.ValidatePassword(req.Password, email); err != nil {
			writeUnprocessable(w, err)
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

type AdminUserStore interface {
	ListUsers(ctx context.Context) ([]account.UserSummary, error)
	PatchUser(ctx context.Context, id string, patch account.UserPatch) (account.User, bool, error)
}

func ListUsers(store AdminUserStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		users, err := store.ListUsers(r.Context())
		if err != nil {
			writeInternalError(r.Context(), w, "listing users", err)
			return
		}
		if users == nil {
			users = []account.UserSummary{}
		}
		writeJSON(w, http.StatusOK, users)
	}
}

type patchUserRequest struct {
	IsAdmin  *bool `json:"is_admin"`
	Disabled *bool `json:"disabled"`
}

const maxPatchUserBodyBytes = 4 << 10

func PatchUser(store AdminUserStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxPatchUserBodyBytes)
		var req patchUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		if req.IsAdmin == nil && req.Disabled == nil {
			writeError(w, http.StatusBadRequest, "nothing to change: send is_admin or disabled")
			return
		}

		// An admin only loses their own access at another admin's hand, which
		// is what keeps the last admin from locking everyone out.
		id := r.PathValue("id")
		if id == caller.ID {
			if req.IsAdmin != nil && !*req.IsAdmin {
				writeError(w, http.StatusConflict, "an admin cannot demote themselves")
				return
			}
			if req.Disabled != nil && *req.Disabled {
				writeError(w, http.StatusConflict, "an admin cannot disable themselves")
				return
			}
		}

		user, found, err := store.PatchUser(r.Context(), id, account.UserPatch{
			IsAdmin:  req.IsAdmin,
			Disabled: req.Disabled,
		})
		if err != nil {
			writeInternalError(r.Context(), w, "updating user", err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeJSON(w, http.StatusOK, user)
	}
}
