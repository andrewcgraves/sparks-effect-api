package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// SeededGraphStore is the slice of the repository the public seeded isochrone
// needs: the scenario the caller named, the compile job whose result is that
// scenario's graph, and somewhere to record the routing job it enqueues.
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

// Isochrone returns an HTTP handler for POST /api/isochrone.
//
// It no longer computes anything. The isochrone is resolved down to one
// immutable compiled graph, handed to the routing worker over the queue, and
// answered 202 with the routing job the caller polls at
// GET /api/routing-jobs/{id} (SPA-182). Valhalla is reachable only from inside
// the home cluster, so the fan-out has to run there and this process cannot do
// it.
//
// The graph comes from the latest succeeded compile job for the scenario, the
// same way the user-authored isochrones resolve theirs (SPA-181) — a graph is
// identified by the compile job that produced it, which is what the message
// names and what the worker's cache keys on. A scenario that has never compiled
// answers 404 rather than falling back: booting seeds and compiles together
// (transit.CompileSeededIfNeeded), so in a deployed environment there is always
// a graph to find.
//
// The routing job it mints has no owner, because this endpoint is public and
// there is no identity to record. See RoutingJobStatus for what that means for
// who may poll it.
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

// loadSeededCompile resolves a seeded scenario's compile job by slug, writing the
// 404 and reporting ok=false when there is none.
//
// It returns the job rather than just its graph because a routing job names the
// compile job, not the bytes: that id is what makes the request reproducible
// and what the worker keys its result cache on.
//
// It checks the scenario exists first so an unknown slug and a known scenario
// that has never compiled are told apart: the first is the caller's mistake,
// the second is a deployment that has not finished coming up, and only the
// second is worth retrying.
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
