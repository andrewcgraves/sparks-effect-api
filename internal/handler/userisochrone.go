package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/isochrone"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/stadia"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// StaleGraphErrorCode is the machine-readable code a client checks to tell a
// stale compiled graph apart from any other 409 or error body: fire the
// compile endpoint, poll, and retry (SPA-83 decision 4).
const StaleGraphErrorCode = "stale_graph"

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
	return req, validateIsochroneParams(w, req.BudgetMins, req.Mode)
}

// validateIsochroneParams checks the budget and mode every isochrone request
// carries, whatever else its body holds — the seeded request names its scenario
// there, the authored ones take theirs from the path.
func validateIsochroneParams(w http.ResponseWriter, budgetMins int, mode string) bool {
	if budgetMins <= 0 {
		writeError(w, http.StatusBadRequest, "budget_mins must be greater than 0")
		return false
	}
	switch isochrone.Mode(mode) {
	case isochrone.ModeWalk, isochrone.ModeBike, isochrone.ModeDrive:
	default:
		writeError(w, http.StatusBadRequest, "invalid mode: must be walk, bike, or drive")
		return false
	}
	return true
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
// request's compiled graph, since a Chainer is fixed to a single IsochroneData
// at construction and the graph is only known per request. The seeded
// /api/isochrone builds its own the same way (SPA-181).
func UserScenarioIsochrone(store ScenarioTargetStore, stadiaClient stadia.Client, log *logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authoredTargetIsochrone(w, r, scenarioTarget{store}, stadiaClient, log)
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
func UserServiceIsochrone(store ServiceTargetStore, stadiaClient stadia.Client, log *logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authoredTargetIsochrone(w, r, serviceTarget{store}, stadiaClient, log)
	}
}

// authoredTargetIsochrone computes an isochrone over one authored target's
// compiled graph: the shared body of both isochrone handlers.
func authoredTargetIsochrone(w http.ResponseWriter, r *http.Request, target authoredTarget,
	stadiaClient stadia.Client, log *logger.Logger) {
	req, ok := validateIsochroneRequest(w, r)
	if !ok {
		return
	}

	owned, ok := target.load(w, r)
	if !ok {
		return
	}
	noun := owned.noun()

	job, ok := loadCompiledGraph(w, r, owned)
	if !ok {
		return
	}

	memberIDs, members, err := owned.members(r.Context())
	if err != nil {
		writeInternalError(w, "loading member services", err)
		return
	}
	if transit.GraphStale(job, memberIDs, updatedAtByID(members)) {
		writeErrorCode(w, http.StatusConflict, StaleGraphErrorCode,
			"compiled graph is stale; recompile the "+noun+" and retry")
		return
	}

	chainer := isochrone.New(stadiaClient, transit.CompiledGraphData{Graph: job.Result}, log)
	resp, err := chainer.Chain(r.Context(), isochrone.ChainRequest{
		Lat:        req.Lat,
		Lng:        req.Lng,
		BudgetMins: req.BudgetMins,
		Mode:       isochrone.Mode(req.Mode),
		// CompiledGraphData is already scoped to this one graph and ignores the
		// slug on both its lookups, so the target's slug resolves nothing here.
		// Two places do still read it, neither fatal but both worth knowing:
		// the chainer's `skipWait` literal (chainer.go:236), so a target whose
		// name happens to slug to "ca-hsr" would silently be chained wait-free;
		// and ChainMetadata.ScenarioSlug, so a single service's response reports
		// its slug under "scenario_slug". Both belong to the wait model SPA-110
		// is to settle — recorded, not fixed here.
		ScenarioSlug: owned.slug(),
	})
	if err != nil {
		log.Debugf("user %s isochrone chain error: %v", noun, err)
		writeChainError(w, err, noun+" not found")
		return
	}

	writeJSON(w, http.StatusOK, resp)
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
