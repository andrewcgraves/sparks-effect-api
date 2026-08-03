package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/isochrone"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/stadia"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// SeededGraphStore is the slice of the repository the public seeded isochrone
// needs: the scenario the caller named, and the compile job whose result is
// that scenario's graph.
type SeededGraphStore interface {
	GetScenarioBySlug(ctx context.Context, slug string) (transit.Scenario, bool, error)
	SeededGraphReader
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
// It resolves the scenario's transit data from the latest succeeded compile job
// (SPA-181), the same way the user-authored isochrones resolve theirs — a graph
// is identified by the compile job that produced it, and a seeded scenario is
// no longer the exception that reads from the embedded store instead. A
// scenario that has never compiled answers 404 rather than falling back:
// booting seeds and compiles together (transit.CompileSeededIfNeeded), so in a
// deployed environment there is always a graph to find.
//
// stadiaClient is threaded through rather than a ready-made Chainer because the
// graph is only known per request, and a Chainer is fixed to one IsochroneData
// at construction.
func Isochrone(store SeededGraphStore, stadiaClient stadia.Client, log *logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req isochroneRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		log.Debugf("isochrone request: lat=%.6f lng=%.6f budget_mins=%d mode=%s scenario=%s",
			req.Lat, req.Lng, req.BudgetMins, req.Mode, req.ScenarioSlug)

		if !validateIsochroneParams(w, req.BudgetMins, req.Mode) {
			return
		}

		graph, ok := loadSeededGraph(w, r, store, req.ScenarioSlug)
		if !ok {
			return
		}

		chainer := isochrone.New(stadiaClient, transit.CompiledGraphData{Graph: graph}, log)
		resp, err := chainer.Chain(r.Context(), isochrone.ChainRequest{
			Lat:          req.Lat,
			Lng:          req.Lng,
			BudgetMins:   req.BudgetMins,
			Mode:         isochrone.Mode(req.Mode),
			ScenarioSlug: req.ScenarioSlug,
		})
		if err != nil {
			log.Debugf("isochrone chain error: %v", err)
			writeChainError(w, err, "scenario not found")
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// loadSeededGraph resolves a seeded scenario's compiled graph by slug, writing
// the 404 and reporting ok=false when there is none.
//
// It checks the scenario exists first so an unknown slug and a known scenario
// that has never compiled are told apart: the first is the caller's mistake,
// the second is a deployment that has not finished coming up, and only the
// second is worth retrying.
func loadSeededGraph(w http.ResponseWriter, r *http.Request, store SeededGraphStore, slug string) (*transit.TransitGraph, bool) {
	if _, found, err := store.GetScenarioBySlug(r.Context(), slug); err != nil {
		writeInternalError(w, "looking up scenario", err)
		return nil, false
	} else if !found {
		writeError(w, http.StatusNotFound, "scenario not found")
		return nil, false
	}

	return latestSeededGraph(w, r, store, slug)
}
