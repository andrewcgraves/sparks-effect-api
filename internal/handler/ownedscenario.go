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

type OwnedScenarioStore interface {
	CreateScenario(ctx context.Context, sc transit.Scenario) error
	GetScenarioBySlug(ctx context.Context, slug string) (transit.Scenario, bool, error)
	UpdateScenario(ctx context.Context, sc transit.Scenario) error
	DeleteScenario(ctx context.Context, id string) error
	CountUnownedScenarioChildren(ctx context.Context, scenarioID string) (int, error)
}

const maxOwnedScenarioBodyBytes = 1 << 20

type ownedScenarioRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

func (req ownedScenarioRequest) applyTo(sc *transit.Scenario) {
	sc.Name = strings.TrimSpace(req.Name)
	sc.Description = req.Description
	sc.Status = req.Status
}

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

func GetOwnedScenario(store OwnedScenarioStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, ok := loadOwnedScenario(w, r, store)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, sc)
	}
}

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
