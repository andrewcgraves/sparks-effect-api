package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// ScenarioStore is the slice of the repository the user-scenario CRUD handlers
// need. It is narrower than transit.Repository so these handlers can be tested
// against a small fake.
type ScenarioStore interface {
	CreateUserScenario(ctx context.Context, sc transit.UserScenario) error
	GetUserScenarioByID(ctx context.Context, id string) (transit.UserScenario, bool, error)
	GetUserScenarioBySlug(ctx context.Context, slug string) (transit.UserScenario, bool, error)
	ListUserScenariosByOwner(ctx context.Context, ownerID string) ([]transit.UserScenario, error)
	UpdateUserScenario(ctx context.Context, sc transit.UserScenario) error
	DeleteUserScenario(ctx context.Context, id string) error
	// UserServiceIDsOwnedBy backs membership validation: a scenario may only
	// curate services the caller owns.
	UserServiceIDsOwnedBy(ctx context.Context, ownerID string, ids []string) (map[string]bool, error)
}

// maxScenarioBodyBytes caps a request body. A curated set of service ids stays
// well under this; anything larger is a client bug or an attack.
const maxScenarioBodyBytes = 1 << 20 // 1 MiB

// userScenarioRequest is the client-writable surface of a scenario. Identity
// fields (id, slug, owner_id) are deliberately absent: the server assigns
// them, so a client cannot claim an ID or reassign ownership by including
// them.
type userScenarioRequest struct {
	Name             string                    `json:"name"`
	Description      string                    `json:"description"`
	ServiceIDs       []string                  `json:"service_ids"`
	InterchangePairs []transit.InterchangePair `json:"interchange_pairs"`
}

// applyTo copies the client-writable fields onto sc, leaving ID, Slug, and
// OwnerID untouched.
func (req userScenarioRequest) applyTo(sc *transit.UserScenario) {
	sc.Name = req.Name
	sc.Description = req.Description
	sc.ServiceIDs = req.ServiceIDs
	sc.InterchangePairs = req.InterchangePairs
}

// CreateUserScenario persists a new curated scenario owned by the caller.
//
// The owner is the identity the middleware resolved from the bearer token, so
// there is no field a client can set to create a scenario in someone else's
// name.
func CreateUserScenario(store ScenarioStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		req, ok := decodeScenarioRequest(w, r)
		if !ok {
			return
		}

		id, err := ids.NewUUID()
		if err != nil {
			writeInternalError(w, "minting scenario id", err)
			return
		}

		sc := transit.UserScenario{ID: id, OwnerID: user.ID}
		req.applyTo(&sc)

		if !validateScenario(w, r, store, sc) {
			return
		}

		slug, err := mintScenarioSlug(r.Context(), store, sc.Name)
		if err != nil {
			writeInternalError(w, "minting slug", err)
			return
		}
		sc.Slug = slug

		if err := store.CreateUserScenario(r.Context(), sc); err != nil {
			writeInternalError(w, "creating scenario", err)
			return
		}

		// Re-read so the response carries the database-assigned timestamps
		// rather than the zero values on the struct we just wrote.
		if stored, found, err := store.GetUserScenarioByID(r.Context(), sc.ID); err == nil && found {
			sc = stored
		}

		w.Header().Set("Location", "/api/user-scenarios/"+sc.Slug)
		writeJSON(w, http.StatusCreated, sc)
	}
}

// GetUserScenario returns one scenario by slug. Reads are owner-scoped: like a
// user service, a scenario is authored content, visible only to its owner (and
// to admins, per auth.CanAccess).
func GetUserScenario(store ScenarioStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, ok := loadScenario(w, r, store)
		if !ok {
			return
		}
		if !authorizeScenario(w, r, sc) {
			return
		}
		writeJSON(w, http.StatusOK, sc)
	}
}

// MyUserScenarios returns the scenarios owned by the caller.
func MyUserScenarios(store ScenarioStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		scenarios, err := store.ListUserScenariosByOwner(r.Context(), user.ID)
		if err != nil {
			writeInternalError(w, "listing scenarios", err)
			return
		}
		if scenarios == nil {
			scenarios = []transit.UserScenario{}
		}
		writeJSON(w, http.StatusOK, scenarios)
	}
}

// UpdateUserScenario replaces a scenario's name, description, and membership.
// The caller must own it.
func UpdateUserScenario(store ScenarioStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, ok := loadScenario(w, r, store)
		if !ok {
			return
		}
		if !authorizeScenario(w, r, sc) {
			return
		}

		req, ok := decodeScenarioRequest(w, r)
		if !ok {
			return
		}
		// applyTo touches only client-writable fields, so ID, Slug, and
		// OwnerID carry over from the stored scenario.
		req.applyTo(&sc)

		if !validateScenario(w, r, store, sc) {
			return
		}
		if err := store.UpdateUserScenario(r.Context(), sc); err != nil {
			writeInternalError(w, "updating scenario", err)
			return
		}

		if stored, found, err := store.GetUserScenarioByID(r.Context(), sc.ID); err == nil && found {
			sc = stored
		}
		writeJSON(w, http.StatusOK, sc)
	}
}

// DeleteUserScenario removes a scenario the caller owns.
func DeleteUserScenario(store ScenarioStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, ok := loadScenario(w, r, store)
		if !ok {
			return
		}
		if !authorizeScenario(w, r, sc) {
			return
		}
		if err := store.DeleteUserScenario(r.Context(), sc.ID); err != nil {
			writeInternalError(w, "deleting scenario", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// loadScenario resolves the {slug} path value, writing 404 or 500 and
// reporting false when it cannot.
func loadScenario(w http.ResponseWriter, r *http.Request, store ScenarioStore) (transit.UserScenario, bool) {
	sc, found, err := store.GetUserScenarioBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeInternalError(w, "loading scenario", err)
		return transit.UserScenario{}, false
	}
	if !found {
		writeError(w, http.StatusNotFound, "scenario not found")
		return transit.UserScenario{}, false
	}
	return sc, true
}

// authorizeScenario applies the shared ownership rule to a loaded scenario.
//
// It answers 404 rather than 403 so a non-owner cannot probe which slugs
// exist — the same rationale as the user-service equivalent.
func authorizeScenario(w http.ResponseWriter, r *http.Request, sc transit.UserScenario) bool {
	user, ok := auth.UserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return false
	}
	// OwnerID is NOT NULL for a user scenario, so the pointer is never nil
	// here; CanAccess takes one to cover the unowned curated rows elsewhere.
	if !auth.CanAccess(user, &sc.OwnerID) {
		writeError(w, http.StatusNotFound, "scenario not found")
		return false
	}
	return true
}

func decodeScenarioRequest(w http.ResponseWriter, r *http.Request) (userScenarioRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxScenarioBodyBytes)

	var req userScenarioRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return userScenarioRequest{}, false
		}
		writeError(w, http.StatusBadRequest, "request body is not valid JSON")
		return userScenarioRequest{}, false
	}
	return req, true
}

// validateScenario applies domain validation, then confirms every named
// service exists and is owned by sc.OwnerID — the "curated set", not
// auto-inclusion: a scenario may reference only services its owner owns, and
// only the ones explicitly listed.
func validateScenario(w http.ResponseWriter, r *http.Request, store ScenarioStore, sc transit.UserScenario) bool {
	if err := sc.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return false
	}
	if len(sc.ServiceIDs) == 0 {
		return true
	}
	owned, err := store.UserServiceIDsOwnedBy(r.Context(), sc.OwnerID, sc.ServiceIDs)
	if err != nil {
		writeInternalError(w, "checking service ownership", err)
		return false
	}
	for _, id := range sc.ServiceIDs {
		if !owned[id] {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("unknown or unauthorized service_id: %s", id))
			return false
		}
	}
	return true
}

// mintScenarioSlug derives a URL-safe slug from name, appending -2, -3, ...
// until it finds one no scenario is using.
//
// This is check-then-insert, so two concurrent creates of the same name can
// both see a slug free and the loser will fail the UNIQUE constraint with a
// 500. Acceptable at present scale; the constraint is what keeps it correct.
func mintScenarioSlug(ctx context.Context, store ScenarioStore, name string) (string, error) {
	base := transit.Slugify(name)
	for attempt := 1; attempt <= maxSlugAttempts; attempt++ {
		candidate := base
		if attempt > 1 {
			candidate = fmt.Sprintf("%s-%d", base, attempt)
		}
		_, taken, err := store.GetUserScenarioBySlug(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no free slug for %q after %d attempts", base, maxSlugAttempts)
}
