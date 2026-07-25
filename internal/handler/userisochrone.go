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

// UserServiceIsochroneStore is the slice of the repository
// UserServiceIsochrone needs: resolving and owning the service (ServiceStore,
// shared with the service CRUD), and the latest compiled graph to stale-check
// and compute over.
//
// Unlike UserIsochroneStore it has no ListUserServicesByIDs: a service is
// always its own sole member, so the record loaded to authorize the request
// already carries the UpdatedAt that staleness turns on.
type UserServiceIsochroneStore interface {
	ServiceStore
	GetLatestSucceededUserServiceJob(ctx context.Context, userServiceSlug string) (transit.Job, bool, error)
}

type userIsochroneRequest struct {
	Lat        float64 `json:"lat"`
	Lng        float64 `json:"lng"`
	BudgetMins int     `json:"budget_mins"`
	Mode       string  `json:"mode"`
}

// validateIsochroneRequest decodes and validates the request body shared by the
// scenario and single-service isochrones, writing a 400 and reporting ok=false
// on anything malformed.
//
// Validation runs before the target is resolved, so a bad body is a 400 even
// for a slug the caller does not own — the body is wrong regardless of who is
// asking, and answering 404 first would make the two indistinguishable.
func validateIsochroneRequest(w http.ResponseWriter, r *http.Request) (userIsochroneRequest, bool) {
	var req userIsochroneRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return req, false
	}
	if req.BudgetMins <= 0 {
		writeError(w, http.StatusBadRequest, "budget_mins must be greater than 0")
		return req, false
	}
	switch isochrone.Mode(req.Mode) {
	case isochrone.ModeWalk, isochrone.ModeBike, isochrone.ModeDrive:
	default:
		writeError(w, http.StatusBadRequest, "invalid mode: must be walk, bike, or drive")
		return req, false
	}
	return req, true
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
		req, ok := validateIsochroneRequest(w, r)
		if !ok {
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
			writeChainError(w, err, "scenario not found")
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// UserServiceIsochrone returns a handler for
// POST /api/services/{slug}/isochrone: an isochrone computed over a single
// service's compiled graph.
//
// The twin of UserScenarioIsochrone, differing only in target — one service,
// compiled alone as the degenerate one-member scenario. Owner-scoped (404, not
// 403, for a non-owner — see authorizeService), and answers 409 with
// StaleGraphErrorCode rather than rendering a graph that no longer reflects the
// service's current definition.
//
// Staleness routes through transit.GraphStale with a one-element membership set
// rather than a separate rule, so both surfaces inherit the same corrections.
// In practice that reduces to the timestamp arm: a service is always its own
// sole member, so it can only go stale by being edited — less often than a
// scenario, which is also stale on a membership change or any member's edit.
func UserServiceIsochrone(store UserServiceIsochroneStore, stadiaClient stadia.Client, log *logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := validateIsochroneRequest(w, r)
		if !ok {
			return
		}

		svc, ok := loadService(w, r, store)
		if !ok {
			return
		}
		if !authorizeService(w, r, svc) {
			return
		}

		job, found, err := store.GetLatestSucceededUserServiceJob(r.Context(), svc.Slug)
		if err != nil {
			writeInternalError(w, "looking up compiled graph", err)
			return
		}
		if !found || job.Result == nil {
			writeError(w, http.StatusNotFound, "no compiled graph for this service yet")
			return
		}

		if transit.GraphStale(job, []string{svc.ID}, map[string]time.Time{svc.ID: svc.UpdatedAt}) {
			writeErrorCode(w, http.StatusConflict, StaleGraphErrorCode,
				"compiled graph is stale; recompile the service and retry")
			return
		}

		chainer := isochrone.New(stadiaClient, transit.CompiledGraphData{Graph: job.Result}, log)
		resp, err := chainer.Chain(r.Context(), isochrone.ChainRequest{
			Lat:        req.Lat,
			Lng:        req.Lng,
			BudgetMins: req.BudgetMins,
			Mode:       isochrone.Mode(req.Mode),
			// CompiledGraphData is already scoped to this one graph and ignores
			// the slug on every lookup, so passing a service slug where a
			// scenario slug is named resolves nothing and is safe.
			ScenarioSlug: svc.Slug,
		})
		if err != nil {
			log.Debugf("user service isochrone chain error: %v", err)
			writeChainError(w, err, "service not found")
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// writeChainError maps a chainer failure onto a status code. notFoundMsg names
// the resource for the one arm that reports a missing target, so each handler
// stays word-for-word consistent with its own CRUD.
func writeChainError(w http.ResponseWriter, err error, notFoundMsg string) {
	switch {
	case errors.Is(err, isochrone.ErrInvalidMode):
		writeError(w, http.StatusBadRequest, "invalid mode: must be walk, bike, or drive")
	case errors.Is(err, isochrone.ErrScenarioNotFound):
		writeError(w, http.StatusNotFound, notFoundMsg)
	case errors.Is(err, isochrone.ErrStadiaClientError):
		writeError(w, http.StatusBadRequest, "routing request exceeded service limits")
	case errors.Is(err, isochrone.ErrStadiaRateLimit):
		writeError(w, http.StatusTooManyRequests, "routing service rate limit exceeded")
	case errors.Is(err, isochrone.ErrStadiaUnavailable):
		writeError(w, http.StatusBadGateway, "routing service unavailable")
	default:
		writeInternalError(w, "computing isochrone", err)
	}
}
