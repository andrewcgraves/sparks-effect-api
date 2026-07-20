package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// UserStore is the slice of the repository account provisioning needs.
type UserStore interface {
	CreateUser(ctx context.Context, u transit.User, passwordHash string) error
	GetUserByEmail(ctx context.Context, email string) (transit.User, bool, error)
}

type createUserRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	IsAdmin  bool   `json:"is_admin"`
}

// CreateUser provisions an account. This is the system's only account-creation
// path — it is registered behind RequireAdmin, which is what makes the API
// invite-only: without an existing admin, no account can come into being.
func CreateUser(store UserStore) http.HandlerFunc {
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
			log.Printf("handler: checking existing user failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		} else if exists {
			writeError(w, http.StatusConflict, "an account with that email already exists")
			return
		}

		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			log.Printf("handler: hashing password failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		id, err := ids.NewUUID()
		if err != nil {
			log.Printf("handler: generating user id failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		user := transit.User{
			ID:      id,
			Email:   email,
			Name:    strings.TrimSpace(req.Name),
			IsAdmin: req.IsAdmin,
		}
		if err := store.CreateUser(r.Context(), user, hash); err != nil {
			log.Printf("handler: creating user failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		writeJSON(w, http.StatusCreated, user)
	}
}
