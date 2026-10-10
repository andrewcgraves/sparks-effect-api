package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/traceid"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type AdminHandoverStore interface {
	GetUserServiceBySlug(ctx context.Context, slug string) (transit.UserService, bool, error)
	GetUserByID(ctx context.Context, id string) (account.User, bool, error)
	TransferUserService(ctx context.Context, serviceID, toUserID, forcedBy string) (transit.ServiceHandover, error)
}

type forceHandoverRequest struct {
	UserID string `json:"user_id"`
}

// The audit row as the admin sees it: both parties by id and name, unlike the
// incoming and outgoing views, which each show only the other party.
type forcedHandover struct {
	ID          string     `json:"id"`
	ServiceSlug string     `json:"service_slug"`
	ServiceName string     `json:"service_name"`
	FromUserID  string     `json:"from_user_id"`
	FromName    string     `json:"from_name"`
	ToUserID    string     `json:"to_user_id"`
	ToName      string     `json:"to_name"`
	Status      string     `json:"status"`
	DecidedAt   *time.Time `json:"decided_at"`
	ForcedBy    *string    `json:"forced_by"`
}

// The route is behind RequireAdmin; nothing here re-checks the caller.
func ForceHandover(store AdminHandoverStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		svc, ok := loadService(w, r, store)
		if !ok {
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxHandoverBodyBytes)
		var req forceHandoverRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			writeError(w, http.StatusBadRequest, "request body is not valid JSON")
			return
		}
		if req.UserID == "" {
			writeError(w, http.StatusBadRequest, "user_id is required")
			return
		}
		// The caller is an admin, so unlike the offer there is nothing to hide
		// about which ids are accounts: each refusal says why. A malformed id
		// is refused here rather than failing the uuid cast as a 500.
		const noSuchUser = "user_id does not name an active account"
		if !ids.IsUUID(req.UserID) {
			writeError(w, http.StatusUnprocessableEntity, noSuchUser)
			return
		}
		to, found, err := store.GetUserByID(r.Context(), req.UserID)
		if err != nil {
			writeInternalError(r.Context(), w, "looking up new owner", err)
			return
		}
		if !found || to.DisabledAt != nil {
			writeError(w, http.StatusUnprocessableEntity, noSuchUser)
			return
		}
		if to.ID == svc.OwnerID {
			writeError(w, http.StatusUnprocessableEntity, ErrHandoverToOwner.Error())
			return
		}

		h, err := store.TransferUserService(r.Context(), svc.ID, to.ID, admin.ID)
		var inScenarios *ServiceInScenariosError
		switch {
		case errors.As(err, &inScenarios):
			writeErrorDetail(w, http.StatusConflict, ServiceInScenariosErrorCode, inScenarios.Error(),
				serviceInScenariosDetail{Scenarios: inScenarios.Slugs})
			return
		// Re-checked under the service's lock; either can change between the
		// reads above and the move.
		case errors.Is(err, ErrHandoverToOwner):
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		case errors.Is(err, ErrHandoverRecipientDisabled):
			writeError(w, http.StatusUnprocessableEntity, noSuchUser)
			return
		case err != nil:
			writeInternalError(r.Context(), w, "moving service owner", err)
			return
		}

		trace, _ := traceid.FromContext(r.Context())
		slog.InfoContext(r.Context(), "handover: service moved by an admin",
			"handover_id", h.ID, "service_id", h.UserServiceID, "service_slug", h.ServiceSlug,
			"from_user_id", h.FromUserID, "to_user_id", h.ToUserID, "forced_by", admin.ID, "trace_id", trace)
		writeJSON(w, http.StatusOK, forcedHandover{
			ID: h.ID, ServiceSlug: h.ServiceSlug, ServiceName: h.ServiceName,
			FromUserID: h.FromUserID, FromName: h.FromName, ToUserID: h.ToUserID, ToName: h.ToName,
			Status: h.Status, DecidedAt: h.DecidedAt, ForcedBy: h.ForcedBy,
		})
	}
}
