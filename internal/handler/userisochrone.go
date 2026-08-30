package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
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
//
// The mode set is transit.TravelMode's own, so what the API accepts and what
// the routing_jobs column stores cannot drift apart.
func validateIsochroneParams(w http.ResponseWriter, budgetMins int, mode string) bool {
	if budgetMins <= 0 {
		writeError(w, http.StatusBadRequest, "budget_mins must be greater than 0")
		return false
	}
	if !transit.TravelMode(mode).Valid() {
		writeError(w, http.StatusBadRequest, "invalid mode: must be walk, bike, or drive")
		return false
	}
	return true
}

// ScenarioIsochroneStore is what an isochrone over a user-authored scenario
// needs: the scenario as a compile target, plus somewhere to record the routing
// job it enqueues.
//
// It is a composition rather than two more methods on ScenarioTargetStore
// because the compile trigger and the graph read address the same target and
// have no routing job to record. Widening that interface would oblige every one
// of its callers to satisfy a half they never touch.
type ScenarioIsochroneStore interface {
	ScenarioTargetStore
	RoutingStore
}

// ServiceIsochroneStore is what an isochrone over a single user-authored
// service needs, composed for the same reason as its scenario counterpart: the
// service as a compile target, plus somewhere to record the routing job.
type ServiceIsochroneStore interface {
	ServiceTargetStore
	RoutingStore
}

// UserScenarioIsochrone returns a handler for
// POST /api/user-scenarios/{slug}/isochrone: an isochrone over a user-built
// scenario's compiled graph, enqueued for the routing worker.
//
// Owner-scoped like the rest of user-scenario CRUD (404, not 403, for a
// non-owner — see authorizeScenario). Answers 409 with StaleGraphErrorCode
// rather than enqueueing work over a graph that no longer reflects the
// scenario's current membership; see transit.GraphStale for what "stale" means
// and why. Since SPA-182 it answers 202 with a routing job rather than a
// computed result — the seeded /api/isochrone does the same.
func UserScenarioIsochrone(store ScenarioIsochroneStore, publisher routing.Publisher, log *slog.Logger, boardingWait transit.BoardingWaitPolicy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authoredTargetIsochrone(w, r, scenarioTarget{store}, store, publisher, log, boardingWait)
	}
}

// UserServiceIsochrone returns a handler for
// POST /api/services/{slug}/isochrone: an isochrone over a single service's
// compiled graph, enqueued for the routing worker.
//
// The twin of UserScenarioIsochrone, differing only in target — one service,
// compiled alone as the degenerate one-member scenario. Owner-scoped (404, not
// 403, for a non-owner — see authorizeService), and answers 409 with
// StaleGraphErrorCode rather than enqueueing over a graph that no longer
// reflects the service's current definition.
//
// Staleness routes through transit.GraphStale with a one-element membership set
// rather than a separate rule, so both surfaces inherit the same corrections.
// In practice that reduces to the timestamp arm: a service is always its own
// sole member, so it can only go stale by being edited — less often than a
// scenario, which is also stale on a membership change or any member's edit.
func UserServiceIsochrone(store ServiceIsochroneStore, publisher routing.Publisher, log *slog.Logger, boardingWait transit.BoardingWaitPolicy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authoredTargetIsochrone(w, r, serviceTarget{store}, store, publisher, log, boardingWait)
	}
}

// authoredTargetIsochrone enqueues an isochrone over one authored target's
// compiled graph: the shared body of both isochrone handlers.
//
// The order matters. Nothing is recorded and nothing is published until the
// caller has been confirmed as the owner and the graph has been confirmed
// current — a stale target must never leave a routing job behind for a worker
// to compute over a graph the owner has already superseded.
func authoredTargetIsochrone(w http.ResponseWriter, r *http.Request, target authoredTarget,
	routingStore RoutingStore, publisher routing.Publisher, log *slog.Logger, boardingWait transit.BoardingWaitPolicy) {
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
		writeInternalError(r.Context(), w, "loading member services", err)
		return
	}
	if transit.GraphStale(job, memberIDs, updatedAtByID(members),
		resolvedBoardingWaitByService(members, owned.scenarioBoardingWait(), boardingWait)) {
		writeErrorCode(w, http.StatusConflict, StaleGraphErrorCode,
			"compiled graph is stale; recompile the "+noun+" and retry")
		return
	}

	// target.load has already established the caller owns this target, so the
	// identity is present; the routing job records it so only they (or an
	// admin) can poll it back.
	user, ok := auth.UserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	log.Debug("enqueueing isochrone", "target", noun, "slug", owned.slug(),
		"lat", req.Lat, "lng", req.Lng, "budget_mins", req.BudgetMins, "mode", req.Mode)

	enqueueIsochrone(w, r, routingStore, publisher, transit.RoutingJob{
		CompileJobID: job.ID,
		OwnerID:      &user.ID,
		Lat:          req.Lat,
		Lng:          req.Lng,
		BudgetMins:   req.BudgetMins,
		Mode:         transit.TravelMode(req.Mode),
	}, job.Result)
}
