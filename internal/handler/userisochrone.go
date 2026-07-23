package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/isochrone"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/stadia"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// StaleGraphErrorCode is the machine-readable code a client checks to tell a
// stale compiled graph apart from any other 409 or error body: fire the
// compile endpoint, poll, and retry (SPA-83 decision 4).
const StaleGraphErrorCode = "stale_graph"

// UserIsochroneStore is the slice of the repository UserScenarioIsochrone
// needs: resolving and owning the scenario (ScenarioStore, shared with the
// rest of the user-scenario CRUD), the member services' current timestamps,
// and the latest compiled graph to stale-check and compute over.
type UserIsochroneStore interface {
	ScenarioStore
	ListUserServicesByIDs(ctx context.Context, ids []string) ([]transit.UserService, error)
	GetLatestSucceededUserScenarioJob(ctx context.Context, userScenarioSlug string) (transit.Job, bool, error)
}

type userIsochroneRequest struct {
	Lat        float64 `json:"lat"`
	Lng        float64 `json:"lng"`
	BudgetMins int     `json:"budget_mins"`
	Mode       string  `json:"mode"`
}

// UserScenarioIsochrone returns a handler for
// POST /api/user-scenarios/{slug}/isochrone: an isochrone computed over a
// user-built scenario's compiled graph.
//
// Owner-scoped like the rest of user-scenario CRUD (404, not 403, for a
// non-owner — see authorizeScenario). Answers 409 with StaleGraphErrorCode
// rather than rendering a graph that no longer reflects the scenario's
// current membership; see transit.GraphStale for what "stale" means and why.
// stadiaClient is threaded through to build a Chainer scoped to this one
// request's compiled graph — the production Chainer, which the seeded
// /api/isochrone owns, cannot be reused because it is fixed to a different
// IsochroneData at construction.
func UserScenarioIsochrone(store UserIsochroneStore, stadiaClient stadia.Client, log *logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req userIsochroneRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.BudgetMins <= 0 {
			writeError(w, http.StatusBadRequest, "budget_mins must be greater than 0")
			return
		}
		switch isochrone.Mode(req.Mode) {
		case isochrone.ModeWalk, isochrone.ModeBike, isochrone.ModeDrive:
		default:
			writeError(w, http.StatusBadRequest, "invalid mode: must be walk, bike, or drive")
			return
		}

		sc, ok := loadScenario(w, r, store)
		if !ok {
			return
		}
		if !authorizeScenario(w, r, sc) {
			return
		}

		job, found, err := store.GetLatestSucceededUserScenarioJob(r.Context(), sc.Slug)
		if err != nil {
			writeInternalError(w, "looking up compiled graph", err)
			return
		}
		if !found || job.Result == nil {
			writeError(w, http.StatusNotFound, "no compiled graph for this scenario yet")
			return
		}

		members, err := store.ListUserServicesByIDs(r.Context(), sc.ServiceIDs)
		if err != nil {
			writeInternalError(w, "loading member services", err)
			return
		}
		updatedAt := make(map[string]time.Time, len(members))
		for _, m := range members {
			updatedAt[m.ID] = m.UpdatedAt
		}
		if transit.GraphStale(job, sc.ServiceIDs, updatedAt) {
			writeErrorCode(w, http.StatusConflict, StaleGraphErrorCode,
				"compiled graph is stale; recompile the scenario and retry")
			return
		}

		chainer := isochrone.New(stadiaClient, transit.CompiledGraphData{Graph: job.Result}, log)
		resp, err := chainer.Chain(r.Context(), isochrone.ChainRequest{
			Lat:          req.Lat,
			Lng:          req.Lng,
			BudgetMins:   req.BudgetMins,
			Mode:         isochrone.Mode(req.Mode),
			ScenarioSlug: sc.Slug,
		})
		if err != nil {
			log.Debugf("user scenario isochrone chain error: %v", err)
			switch {
			case errors.Is(err, isochrone.ErrInvalidMode):
				writeError(w, http.StatusBadRequest, "invalid mode: must be walk, bike, or drive")
			case errors.Is(err, isochrone.ErrScenarioNotFound):
				writeError(w, http.StatusNotFound, "scenario not found")
			case errors.Is(err, isochrone.ErrStadiaClientError):
				writeError(w, http.StatusBadRequest, "routing request exceeded service limits")
			case errors.Is(err, isochrone.ErrStadiaRateLimit):
				writeError(w, http.StatusTooManyRequests, "routing service rate limit exceeded")
			case errors.Is(err, isochrone.ErrStadiaUnavailable):
				writeError(w, http.StatusBadGateway, "routing service unavailable")
			default:
				writeInternalError(w, "computing isochrone", err)
			}
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}
