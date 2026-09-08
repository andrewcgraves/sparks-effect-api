package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const StaleGraphErrorCode = "stale_graph"

type userIsochroneRequest struct {
	Lat        float64 `json:"lat"`
	Lng        float64 `json:"lng"`
	BudgetMins int     `json:"budget_mins"`
	Mode       string  `json:"mode"`
}

func validateIsochroneRequest(w http.ResponseWriter, r *http.Request) (userIsochroneRequest, bool) {
	var req userIsochroneRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return req, false
	}
	return req, validateIsochroneParams(w, req.BudgetMins, req.Mode)
}

func validateIsochroneParams(w http.ResponseWriter, budgetMins int, mode string) bool {
	if budgetMins <= 0 {
		writeError(w, http.StatusBadRequest, "budget_mins must be greater than 0")
		return false
	}
	if !transit.TravelMode(mode).Valid() {
		writeError(w, http.StatusBadRequest, "invalid mode: must be one of "+transit.TravelModeList())
		return false
	}
	return true
}

type ScenarioIsochroneStore interface {
	ScenarioTargetStore
	RoutingStore
}

type ServiceIsochroneStore interface {
	ServiceTargetStore
	RoutingStore
}

func UserScenarioIsochrone(store ScenarioIsochroneStore, publisher routing.Publisher, log *slog.Logger, boardingWait transit.BoardingWaitPolicy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authoredTargetIsochrone(w, r, scenarioTarget{store}, store, publisher, log, boardingWait)
	}
}

func UserServiceIsochrone(store ServiceIsochroneStore, publisher routing.Publisher, log *slog.Logger, boardingWait transit.BoardingWaitPolicy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authoredTargetIsochrone(w, r, serviceTarget{store}, store, publisher, log, boardingWait)
	}
}

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
