package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const HandoverTTL = 14 * 24 * time.Hour

var (
	ErrHandoverPending    = errors.New("service already has a pending handover")
	ErrHandoverNotFound   = errors.New("handover not found")
	ErrHandoverNotPending = errors.New("handover is no longer pending")
)

type HandoverStore interface {
	GetUserServiceBySlug(ctx context.Context, slug string) (transit.UserService, bool, error)
	GetUserByEmail(ctx context.Context, email string) (account.User, bool, error)
	HasPendingServiceHandover(ctx context.Context, serviceID string) (bool, error)
	OfferServiceHandover(ctx context.Context, h transit.ServiceHandover, ttl time.Duration) (transit.ServiceHandover, error)
	ListPendingServiceHandovers(ctx context.Context, userID string) ([]transit.ServiceHandover, error)
	CancelServiceHandover(ctx context.Context, id, fromUserID string) (transit.ServiceHandover, error)
	DeclineServiceHandover(ctx context.Context, id, toUserID string) (transit.ServiceHandover, error)
}

const maxHandoverBodyBytes = 4 << 10

type offerHandoverRequest struct {
	ToEmail string `json:"to_email"`
}

// No id and no recipient name: the body has to be identical whether or not
// the address belongs to an account, and either would only exist for one.
type offerHandoverResponse struct {
	ToEmail string `json:"to_email"`
}

type incomingHandover struct {
	ID          string     `json:"id"`
	ServiceSlug string     `json:"service_slug"`
	ServiceName string     `json:"service_name"`
	FromName    string     `json:"from_name"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	DecidedAt   *time.Time `json:"decided_at,omitempty"`
}

type outgoingHandover struct {
	ID          string     `json:"id"`
	ServiceSlug string     `json:"service_slug"`
	ServiceName string     `json:"service_name"`
	ToName      string     `json:"to_name"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	DecidedAt   *time.Time `json:"decided_at,omitempty"`
}

type myHandoversResponse struct {
	Incoming []incomingHandover `json:"incoming"`
	Outgoing []outgoingHandover `json:"outgoing"`
}

func incomingView(h transit.ServiceHandover) incomingHandover {
	return incomingHandover{
		ID: h.ID, ServiceSlug: h.ServiceSlug, ServiceName: h.ServiceName, FromName: h.FromName,
		Status: h.Status, CreatedAt: h.CreatedAt, ExpiresAt: h.ExpiresAt, DecidedAt: h.DecidedAt,
	}
}

func outgoingView(h transit.ServiceHandover) outgoingHandover {
	return outgoingHandover{
		ID: h.ID, ServiceSlug: h.ServiceSlug, ServiceName: h.ServiceName, ToName: h.ToName,
		Status: h.Status, CreatedAt: h.CreatedAt, ExpiresAt: h.ExpiresAt, DecidedAt: h.DecidedAt,
	}
}

func OfferHandover(store HandoverStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Authorised before the body is read, so a stranger gets the same 404
		// for a real slug as for a missing one whatever they send.
		svc, ok := loadService(w, r, store)
		if !ok {
			return
		}
		if !authorizeService(w, r, svc) {
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxHandoverBodyBytes)
		var req offerHandoverRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			writeError(w, http.StatusBadRequest, "request body is not valid JSON")
			return
		}
		email := normalizeEmail(req.ToEmail)
		if email == "" {
			writeError(w, http.StatusBadRequest, "to_email is required")
			return
		}

		// Checked before the recipient is looked up, so the 409 is the same
		// for every address and says nothing about which ones are accounts.
		pending, err := store.HasPendingServiceHandover(r.Context(), svc.ID)
		if err != nil {
			writeInternalError(r.Context(), w, "checking pending handover", err)
			return
		}
		if pending {
			writeError(w, http.StatusConflict, ErrHandoverPending.Error())
			return
		}

		to, found, err := store.GetUserByEmail(r.Context(), email)
		if err != nil {
			writeInternalError(r.Context(), w, "looking up handover recipient", err)
			return
		}
		// Only the owner's own address can land here, and only the owner or an
		// admin can ask, so this 422 confirms nothing they did not know.
		if found && to.ID == svc.OwnerID {
			writeError(w, http.StatusUnprocessableEntity, "a service cannot be handed over to its owner")
			return
		}
		accepted := offerHandoverResponse{ToEmail: email}
		// A disabled account could never accept, so it is treated exactly as
		// an unknown address: nothing recorded, the same 202.
		if !found || to.DisabledAt != nil {
			writeJSON(w, http.StatusAccepted, accepted)
			return
		}

		id, err := ids.NewUUID()
		if err != nil {
			writeInternalError(r.Context(), w, "minting handover id", err)
			return
		}
		// The sender is the service's owner even when an admin makes the
		// offer: it is the owner's service that moves, so it is the owner who
		// sees the offer as outgoing and may cancel it.
		_, err = store.OfferServiceHandover(r.Context(), transit.ServiceHandover{
			ID: id, UserServiceID: svc.ID, FromUserID: svc.OwnerID, ToUserID: to.ID,
		}, HandoverTTL)
		if errors.Is(err, ErrHandoverPending) {
			writeError(w, http.StatusConflict, ErrHandoverPending.Error())
			return
		}
		if err != nil {
			writeInternalError(r.Context(), w, "offering handover", err)
			return
		}
		writeJSON(w, http.StatusAccepted, accepted)
	}
}

func MyHandovers(store HandoverStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		handovers, err := store.ListPendingServiceHandovers(r.Context(), user.ID)
		if err != nil {
			writeInternalError(r.Context(), w, "listing handovers", err)
			return
		}
		resp := myHandoversResponse{Incoming: []incomingHandover{}, Outgoing: []outgoingHandover{}}
		for _, h := range handovers {
			if h.ToUserID == user.ID {
				resp.Incoming = append(resp.Incoming, incomingView(h))
			}
			if h.FromUserID == user.ID {
				resp.Outgoing = append(resp.Outgoing, outgoingView(h))
			}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func CancelHandover(store HandoverStore) http.HandlerFunc {
	return decideHandover(store.CancelServiceHandover, func(w http.ResponseWriter, h transit.ServiceHandover) {
		writeJSON(w, http.StatusOK, outgoingView(h))
	})
}

func DeclineHandover(store HandoverStore) http.HandlerFunc {
	return decideHandover(store.DeclineServiceHandover, func(w http.ResponseWriter, h transit.ServiceHandover) {
		writeJSON(w, http.StatusOK, incomingView(h))
	})
}

func decideHandover(
	decide func(ctx context.Context, id, callerID string) (transit.ServiceHandover, error),
	respond func(http.ResponseWriter, transit.ServiceHandover),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		id := r.PathValue("id")
		// A malformed id cannot name a row; answering 404 here keeps it from
		// reaching the uuid column as a 500.
		if !ids.IsUUID(id) {
			writeError(w, http.StatusNotFound, ErrHandoverNotFound.Error())
			return
		}
		// Admins get no override: only the one party a decision belongs to
		// may make it, and everyone else is told the offer does not exist.
		h, err := decide(r.Context(), id, user.ID)
		switch {
		case errors.Is(err, ErrHandoverNotFound):
			writeError(w, http.StatusNotFound, ErrHandoverNotFound.Error())
		case errors.Is(err, ErrHandoverNotPending):
			writeError(w, http.StatusConflict, ErrHandoverNotPending.Error())
		case err != nil:
			writeInternalError(r.Context(), w, "deciding handover", err)
		default:
			respond(w, h)
		}
	}
}
