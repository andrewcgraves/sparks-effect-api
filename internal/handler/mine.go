package handler

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type OwnerStore interface {
	ListScenariosByOwner(ctx context.Context, ownerID string) ([]transit.Scenario, error)
	ListServicesByOwner(ctx context.Context, ownerID string) ([]transit.Service, error)
}

func MyScenarios(store OwnerStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		scenarios, err := store.ListScenariosByOwner(r.Context(), user.ID)
		if err != nil {
			slog.ErrorContext(r.Context(), "handler: listing owned scenarios failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if scenarios == nil {
			scenarios = []transit.Scenario{}
		}
		writeJSON(w, http.StatusOK, scenarios)
	}
}

func MyServices(store OwnerStore, boardingWait transit.BoardingWaitPolicy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		services, err := store.ListServicesByOwner(r.Context(), user.ID)
		if err != nil {
			slog.ErrorContext(r.Context(), "handler: listing owned services failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if services == nil {
			services = []transit.Service{}
		}
		writeJSON(w, http.StatusOK, withBoardingWaits(r.Context(), services, nil, boardingWait))
	}
}
