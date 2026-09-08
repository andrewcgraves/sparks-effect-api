package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type PrerenderedStore interface {
	GetScenarioBySlug(ctx context.Context, slug string) (transit.Scenario, bool, error)
	ListServiceMembershipByScenario(ctx context.Context, scenarioID string) ([]transit.ServiceMembership, error)
	ListPrerenderedIsochronesByScenario(ctx context.Context, scenarioSlug string) ([]transit.PrerenderedIsochrone, error)
	GetPrerenderedIsochrone(ctx context.Context, id string) (transit.PrerenderedIsochrone, bool, error)
	CreatePrerenderedIsochrone(ctx context.Context, p *transit.PrerenderedIsochrone) error
}

type prerenderedResponse struct {
	ID         string             `json:"id"`
	Label      string             `json:"label"`
	Lat        float64            `json:"lat"`
	Lng        float64            `json:"lng"`
	BudgetMins int                `json:"budget_mins"`
	Mode       transit.TravelMode `json:"mode"`
	Outdated   bool               `json:"outdated"`
	CreatedAt  time.Time          `json:"created_at"`
	Result     json.RawMessage    `json:"result,omitempty"`
}

func prerenderedMeta(p transit.PrerenderedIsochrone, outdated bool) prerenderedResponse {
	return prerenderedResponse{
		ID:         p.ID,
		Label:      p.Label,
		Lat:        p.Lat,
		Lng:        p.Lng,
		BudgetMins: p.BudgetMins,
		Mode:       p.Mode,
		Outdated:   outdated,
		CreatedAt:  p.CreatedAt,
	}
}

type prerenderedCreateRequest struct {
	Label      string          `json:"label"`
	Lat        float64         `json:"lat"`
	Lng        float64         `json:"lng"`
	BudgetMins int             `json:"budget_mins"`
	Mode       string          `json:"mode"`
	Result     json.RawMessage `json:"result"`
}

func PrerenderedIsochrones(store PrerenderedStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, ok := lookupScenario(w, r, store, r.PathValue("slug"))
		if !ok {
			return
		}

		entries, err := store.ListPrerenderedIsochronesByScenario(r.Context(), sc.Slug)
		if err != nil {
			writeInternalError(r.Context(), w, "listing prerendered isochrones", err)
			return
		}

		members, ok := loadMembership(w, r, store, sc)
		if !ok {
			return
		}

		out := make([]prerenderedResponse, 0, len(entries))
		for _, p := range entries {
			out = append(out, prerenderedMeta(p, transit.PrerenderedOutdated(p, members)))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func PrerenderedIsochrone(store PrerenderedStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, found, err := store.GetPrerenderedIsochrone(r.Context(), r.PathValue("id"))
		if err != nil {
			writeInternalError(r.Context(), w, "looking up prerendered isochrone", err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "prerendered isochrone not found")
			return
		}

		// The row names its scenario by slug, which is all outdated needs
		// resolving. A missing scenario cannot happen while the row exists —
		// the foreign key CASCADEs — so this is a lookup, not a validation.
		sc, ok := lookupScenario(w, r, store, p.ScenarioSlug)
		if !ok {
			return
		}
		members, ok := loadMembership(w, r, store, sc)
		if !ok {
			return
		}

		out := prerenderedMeta(p, transit.PrerenderedOutdated(p, members))
		out.Result = p.Result
		writeJSON(w, http.StatusOK, out)
	}
}

func CreatePrerenderedIsochrone(store PrerenderedStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req prerenderedCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		req.Label = strings.TrimSpace(req.Label)
		if req.Label == "" {
			writeError(w, http.StatusBadRequest, "label is required")
			return
		}
		// The budget and mode checks are the ones every isochrone request
		// already goes through, so what may be curated and what may be
		// requested cannot drift apart.
		if !validateIsochroneParams(w, req.BudgetMins, req.Mode) {
			return
		}
		if len(req.Result) == 0 {
			writeError(w, http.StatusBadRequest, "result is required")
			return
		}

		sc, ok := lookupScenario(w, r, store, r.PathValue("slug"))
		if !ok {
			return
		}
		members, ok := loadMembership(w, r, store, sc)
		if !ok {
			return
		}

		id, err := ids.NewUUID()
		if err != nil {
			writeInternalError(r.Context(), w, "generating prerendered isochrone id", err)
			return
		}

		entry := transit.PrerenderedIsochrone{
			ID:                 id,
			ScenarioSlug:       sc.Slug,
			Label:              req.Label,
			Lat:                req.Lat,
			Lng:                req.Lng,
			BudgetMins:         req.BudgetMins,
			Mode:               transit.TravelMode(req.Mode),
			Result:             req.Result,
			CompiledServiceIDs: transit.MembershipIDs(members),
		}
		if err := store.CreatePrerenderedIsochrone(r.Context(), &entry); err != nil {
			writeInternalError(r.Context(), w, "creating prerendered isochrone", err)
			return
		}

		// Freshly snapshotted against the membership just read, so it cannot be
		// outdated — stated by construction rather than recomputed.
		writeJSON(w, http.StatusCreated, prerenderedMeta(entry, false))
	}
}

func lookupScenario(w http.ResponseWriter, r *http.Request, store PrerenderedStore, slug string) (transit.Scenario, bool) {
	sc, found, err := store.GetScenarioBySlug(r.Context(), slug)
	if err != nil {
		writeInternalError(r.Context(), w, "looking up scenario", err)
		return transit.Scenario{}, false
	}
	// Curated scenarios only. A prerendered isochrone is editorial content on a
	// public page — authored by an admin, served to anyone — so an owned
	// scenario is simply not a place one can hang. Reported as not found rather
	// than refused, so this endpoint cannot be used to probe which owned slugs
	// exist.
	if !found || sc.OwnerID != nil {
		writeError(w, http.StatusNotFound, "scenario not found")
		return transit.Scenario{}, false
	}
	return sc, true
}

func loadMembership(w http.ResponseWriter, r *http.Request, store PrerenderedStore,
	sc transit.Scenario) ([]transit.ServiceMembership, bool) {
	members, err := store.ListServiceMembershipByScenario(r.Context(), sc.ID)
	if err != nil {
		writeInternalError(r.Context(), w, "loading scenario service membership", err)
		return nil, false
	}
	return members, true
}
