package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/route"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// OwnedScenarioStore is the slice of the repository the owner-scoped scenario
// CRUD needs.
//
// GetScenarioBySlug is unfiltered for GetRouteBySlug's reason: scenarios.slug is
// globally unique across curated and owned rows, so minting a slug has to see
// the whole namespace. Ownership is decided here, against the row it returns.
type OwnedScenarioStore interface {
	CreateScenario(ctx context.Context, sc transit.Scenario) error
	GetScenarioBySlug(ctx context.Context, slug string) (transit.Scenario, bool, error)
	UpdateScenario(ctx context.Context, sc transit.Scenario) error
	DeleteScenario(ctx context.Context, id string) error
	CountUnownedScenarioChildren(ctx context.Context, scenarioID string) (int, error)
}

// maxOwnedScenarioBodyBytes caps a request body. A scenario is three short
// strings; anything larger is a client bug or an attack.
const maxOwnedScenarioBodyBytes = 1 << 20 // 1 MiB

// ownedScenarioRequest is the client-writable surface of a seeded scenario.
// Identity fields (id, slug, owner_id) are deliberately absent: the server
// assigns them, so a client cannot claim an ID or reassign ownership by
// including them.
type ownedScenarioRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

// applyTo copies the client-writable fields onto sc, leaving ID, Slug, and
// OwnerID untouched.
func (req ownedScenarioRequest) applyTo(sc *transit.Scenario) {
	sc.Name = strings.TrimSpace(req.Name)
	sc.Description = req.Description
	sc.Status = req.Status
}

// CreateOwnedScenario persists a new scenario owned by the caller.
//
// The scenario starts empty: its routes, stations, travel-time segments, and
// services are authored afterwards through the endpoints under
// /api/me/scenarios/{slug}. It is not compilable until it has stations and
// segments, which is what a compile will tell the caller if they try early.
func CreateOwnedScenario(store OwnedScenarioStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		req, ok := decodeOwnedScenarioRequest(w, r)
		if !ok {
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeError(w, http.StatusUnprocessableEntity, "name is required")
			return
		}

		id, err := ids.NewUUID()
		if err != nil {
			writeInternalError(r.Context(), w, "minting scenario id", err)
			return
		}

		sc := transit.Scenario{ID: id, OwnerID: &user.ID}
		req.applyTo(&sc)

		slug, err := mintOwnedScenarioSlug(r.Context(), store, sc.Name)
		if err != nil {
			writeInternalError(r.Context(), w, "minting scenario slug", err)
			return
		}
		if slug == "" {
			writeError(w, http.StatusUnprocessableEntity,
				"could not derive a slug from the name; give the scenario a name with letters or digits in it")
			return
		}
		sc.Slug = slug

		if err := store.CreateScenario(r.Context(), sc); err != nil {
			writeInternalError(r.Context(), w, "creating scenario", err)
			return
		}

		w.Header().Set("Location", "/api/me/scenarios/"+sc.Slug)
		writeJSON(w, http.StatusCreated, sc)
	}
}

// GetOwnedScenario returns one of the caller's own scenarios.
func GetOwnedScenario(store OwnedScenarioStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, ok := loadOwnedScenario(w, r, store)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, sc)
	}
}

// UpdateOwnedScenario rewrites the name, description, and status of a scenario
// the caller owns.
//
// The slug is not re-derived from a changed name: it is the scenario's address,
// and it is also what the travel-time set is keyed on, so re-slugging would
// silently detach a scenario from its own segment times.
func UpdateOwnedScenario(store OwnedScenarioStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, ok := loadOwnedScenario(w, r, store)
		if !ok {
			return
		}

		req, ok := decodeOwnedScenarioRequest(w, r)
		if !ok {
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeError(w, http.StatusUnprocessableEntity, "name is required")
			return
		}
		// applyTo touches only client-writable fields, so ID, Slug, and OwnerID
		// carry over from the stored scenario.
		req.applyTo(&sc)

		if err := store.UpdateScenario(r.Context(), sc); err != nil {
			writeInternalError(r.Context(), w, "updating scenario", err)
			return
		}
		writeJSON(w, http.StatusOK, sc)
	}
}

// DeleteOwnedScenario removes a scenario the caller owns, together with
// everything under it.
//
// The cascade is wide — routes, stations, services, segments, the travel-time
// set — and is safe exactly while the ownership-uniformity invariant holds: the
// children of an owned scenario are the caller's own. A curated child means the
// invariant has been broken (an admin attached platform content), so the delete
// is refused rather than cascading over rows the caller does not own.
func DeleteOwnedScenario(store OwnedScenarioStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, ok := loadOwnedScenario(w, r, store)
		if !ok {
			return
		}

		curated, err := store.CountUnownedScenarioChildren(r.Context(), sc.ID)
		if err != nil {
			writeInternalError(r.Context(), w, "counting curated scenario children", err)
			return
		}
		if curated > 0 {
			writeErrorCode(w, http.StatusConflict, "scenario_holds_curated_content",
				"this scenario holds curated rows, so deleting it would remove content you do not own")
			return
		}

		if err := store.DeleteScenario(r.Context(), sc.ID); err != nil {
			writeInternalError(r.Context(), w, "deleting scenario", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// loadOwnedScenario resolves the {slug} path value and applies the ownership
// rule, answering 404 rather than 403 for the reason loadOwnedRoute does. A
// curated scenario lands here too — its owner is nil, so CanAccess admits only
// admins — which is what keeps the ca-hsr baseline read-only.
func loadOwnedScenario(w http.ResponseWriter, r *http.Request, store OwnedScenarioStore) (transit.Scenario, bool) {
	user, ok := auth.UserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return transit.Scenario{}, false
	}

	sc, found, err := store.GetScenarioBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeInternalError(r.Context(), w, "looking up scenario", err)
		return transit.Scenario{}, false
	}
	if !found || !auth.CanAccess(user, sc.OwnerID) {
		writeError(w, http.StatusNotFound, "scenario not found")
		return transit.Scenario{}, false
	}
	return sc, true
}

// loadScenarioToAuthorIn is loadOwnedScenario for the paths that create a child
// row inside the scenario they resolve. It adds mayAuthorInScenario's one extra
// refusal: a curated scenario is not an authoring target on /api/me, admins
// included, because the child would be stamped with an owner its parent does
// not have.
//
// It answers 404, like loadOwnedScenario, rather than distinguishing "you may
// not author here" from "no such scenario".
func loadScenarioToAuthorIn(w http.ResponseWriter, r *http.Request, store OwnedScenarioStore) (transit.Scenario, bool) {
	sc, ok := loadOwnedScenario(w, r, store)
	if !ok {
		return transit.Scenario{}, false
	}
	user, _ := auth.UserFrom(r.Context())
	if !mayAuthorInScenario(user, sc) {
		writeError(w, http.StatusNotFound, "scenario not found")
		return transit.Scenario{}, false
	}
	return sc, true
}

func decodeOwnedScenarioRequest(w http.ResponseWriter, r *http.Request) (ownedScenarioRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxOwnedScenarioBodyBytes)

	var req ownedScenarioRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return ownedScenarioRequest{}, false
		}
		writeError(w, http.StatusBadRequest, "request body is not valid JSON")
		return ownedScenarioRequest{}, false
	}
	return req, true
}

// mintOwnedScenarioSlug derives a slug from name, appending -2, -3, ... until
// it finds one no scenario is using. It returns "" when the name slugifies to
// nothing, which the caller reports as a 422.
//
// The namespace spans curated and owned scenarios alike, because scenarios.slug
// is globally unique: a user naming their scenario "CA HSR" gets ca-hsr-2
// rather than a constraint violation. Check-then-insert, with the same race and
// the same justification as mintSlug.
//
// route.Slugify rather than transit.Slugify: the latter substitutes the literal
// "service" for a name that slugifies to nothing, which is a sensible default
// for a UserService and a wrong one for a scenario. The neutral function
// returns "", and the caller turns that into a 422 telling the author to pick a
// nameable name.
func mintOwnedScenarioSlug(ctx context.Context, store OwnedScenarioStore, name string) (string, error) {
	base := route.Slugify(name)
	if base == "" {
		return "", nil
	}
	for attempt := 1; attempt <= maxSlugAttempts; attempt++ {
		candidate := base
		if attempt > 1 {
			candidate = fmt.Sprintf("%s-%d", base, attempt)
		}
		_, taken, err := store.GetScenarioBySlug(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no free slug for %q after %d attempts", base, maxSlugAttempts)
}
