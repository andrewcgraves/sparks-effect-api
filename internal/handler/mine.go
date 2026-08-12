package handler

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// OwnerStore is the slice of the repository the owner-scoped reads need.
type OwnerStore interface {
	ListScenariosByOwner(ctx context.Context, ownerID string) ([]transit.Scenario, error)
	ListServicesByOwner(ctx context.Context, ownerID string) ([]transit.Service, error)
}

// MyScenarios returns the scenarios owned by the authenticated caller.
//
// The owner ID comes from the request context — the identity the middleware
// resolved from the bearer token — and never from the request itself, so there
// is no parameter a caller could set to read someone else's rows.
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

// MyServices returns the services owned by the authenticated caller. Admins are
// scoped to their own rows here too: admin rights gate privileged endpoints,
// they do not redefine what "mine" means.
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
		writeJSON(w, http.StatusOK, withBoardingWaits(services, boardingWait))
	}
}
