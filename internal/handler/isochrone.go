package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type SeededGraphStore interface {
	GetScenarioBySlug(ctx context.Context, slug string) (transit.Scenario, bool, error)
	SeededGraphReader
	RoutingStore
}

type isochroneRequest struct {
	Lat          float64 `json:"lat"`
	Lng          float64 `json:"lng"`
	BudgetMins   int     `json:"budget_mins"`
	Mode         string  `json:"mode"`
	ScenarioSlug string  `json:"scenario_slug"`
}

func Isochrone(store SeededGraphStore, publisher routing.Publisher, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req isochroneRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		log.Debug("isochrone request", "lat", req.Lat, "lng", req.Lng,
			"budget_mins", req.BudgetMins, "mode", req.Mode, "scenario_slug", req.ScenarioSlug)

		if !validateIsochroneParams(w, req.BudgetMins, req.Mode) {
			return
		}

		job, ok := loadSeededCompile(w, r, store, req.ScenarioSlug)
		if !ok {
			return
		}

		enqueueIsochrone(w, r, store, publisher, transit.RoutingJob{
			CompileJobID: job.ID,
			Lat:          req.Lat,
			Lng:          req.Lng,
			BudgetMins:   req.BudgetMins,
			Mode:         transit.TravelMode(req.Mode),
		}, job.Result)
	}
}

func loadSeededCompile(w http.ResponseWriter, r *http.Request, store SeededGraphStore, slug string) (transit.Job, bool) {
	sc, found, err := store.GetScenarioBySlug(r.Context(), slug)
	if err != nil {
		writeInternalError(r.Context(), w, "looking up scenario", err)
		return transit.Job{}, false
	}
	// An owned scenario is plottable by its owner and invisible to everyone
	// else, the same gate its graph read applies.
	if !found || !mayReachScenario(r.Context(), sc) {
		writeError(w, http.StatusNotFound, "scenario not found")
		return transit.Job{}, false
	}

	return latestSeededCompile(w, r, store, slug)
}
