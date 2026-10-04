package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const StaleGraphErrorCode = "stale_graph"

// Every isochrone request, seeded, authored or published, is a point, a budget
// and a mode: a few hundred bytes. Shared so the three stay one limit.
const maxIsochroneBodyBytes = 4 << 10

// Above the worker's Valhalla contour ceiling (VALHALLA_MAX_ISO_CONTOUR_MINS,
// 320) the chain clamps its contours, and a clamped egress contour no longer
// says which budget it was cut for, which is the case SPA-326's cache key
// cannot fully separate. The largest preset the site offers is 240. 300 keeps
// every budget below the ceiling, so nothing is ever clamped, and leaves room
// for a five-hour preset. Raise the ceiling before raising this.
const maxIsochroneBudgetMins = 300

type userIsochroneRequest struct {
	Lat        float64 `json:"lat"`
	Lng        float64 `json:"lng"`
	BudgetMins int     `json:"budget_mins"`
	Mode       string  `json:"mode"`
}

func validateIsochroneRequest(w http.ResponseWriter, r *http.Request) (userIsochroneRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxIsochroneBodyBytes)

	var req userIsochroneRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return req, false
		}
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
	if budgetMins > maxIsochroneBudgetMins {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("budget_mins must be at most %d", maxIsochroneBudgetMins))
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
