package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/isochrone"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
)

type isochroneRequest struct {
	Lat          float64 `json:"lat"`
	Lng          float64 `json:"lng"`
	BudgetMins   int     `json:"budget_mins"`
	Mode         string  `json:"mode"`
	ScenarioSlug string  `json:"scenario_slug"`
}

// Isochrone returns an HTTP handler for POST /api/isochrone.
func Isochrone(chainer isochrone.Chainer, log *logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req isochroneRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		log.Debugf("isochrone request: lat=%.6f lng=%.6f budget_mins=%d mode=%s scenario=%s",
			req.Lat, req.Lng, req.BudgetMins, req.Mode, req.ScenarioSlug)

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

		resp, err := chainer.Chain(r.Context(), isochrone.ChainRequest{
			Lat:          req.Lat,
			Lng:          req.Lng,
			BudgetMins:   req.BudgetMins,
			Mode:         isochrone.Mode(req.Mode),
			ScenarioSlug: req.ScenarioSlug,
		})
		if err != nil {
			log.Debugf("isochrone chain error: %v", err)
			switch {
			case errors.Is(err, isochrone.ErrInvalidMode):
				writeError(w, http.StatusBadRequest, "invalid mode: must be walk, bike, or drive")
			case errors.Is(err, isochrone.ErrScenarioNotFound):
				writeError(w, http.StatusNotFound, "scenario not found")
			case errors.Is(err, isochrone.ErrStadiaUnavailable):
				writeError(w, http.StatusBadGateway, "routing service unavailable")
			default:
				writeError(w, http.StatusInternalServerError, "internal error")
			}
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}
