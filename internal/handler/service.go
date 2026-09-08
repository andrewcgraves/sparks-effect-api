package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/traceid"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type ServiceStore interface {
	CreateUserService(ctx context.Context, svc transit.UserService) error
	GetUserServiceByID(ctx context.Context, id string) (transit.UserService, bool, error)
	GetUserServiceBySlug(ctx context.Context, slug string) (transit.UserService, bool, error)
	ListUserServicesByOwner(ctx context.Context, ownerID string) ([]transit.UserService, error)
	UpdateUserService(ctx context.Context, svc transit.UserService) error
	DeleteUserService(ctx context.Context, id string) error
	GetRouteBySlug(ctx context.Context, slug string) (transit.Route, bool, error)
}

const maxServiceBodyBytes = 1 << 20

type serviceRequest struct {
	RouteSlug        string                     `json:"route_slug"`
	Name             string                     `json:"name"`
	Description      string                     `json:"description"`
	Vehicle          transit.VehicleParams      `json:"vehicle"`
	Stops            []transit.ServiceStopPoint `json:"stops"`
	FrequencyWindows []transit.FrequencyWindow  `json:"frequency_windows"`
	BoardingWait     optionalBoardingWait       `json:"boarding_wait"`
}

func (req serviceRequest) applyTo(svc *transit.UserService) {
	svc.Name = req.Name
	svc.Description = req.Description
	svc.Vehicle = req.Vehicle
	svc.Stops = req.Stops
	svc.FrequencyWindows = req.FrequencyWindows
	svc.NormalizeStops()
	svc.MintStopSlugs()
	if req.BoardingWait.set {
		svc.BoardingWait = req.BoardingWait.value
	}
}

func CreateService(store ServiceStore, boardingWait transit.BoardingWaitPolicy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		req, ok := decodeServiceRequest(w, r)
		if !ok {
			return
		}

		id, err := ids.NewUUID()
		if err != nil {
			writeInternalError(r.Context(), w, "minting service id", err)
			return
		}

		// The service slug is settled before the request is applied, because a
		// stop's identity is namespaced by it: minting stops first would name them
		// after the slug the service asked for rather than the one it got, so two
		// services whose names collide would mint colliding stop identities.
		slug, err := mintSlug(r.Context(), store, req.Name)
		if err != nil {
			writeInternalError(r.Context(), w, "minting slug", err)
			return
		}

		svc := transit.UserService{ID: id, Slug: slug, OwnerID: user.ID}
		req.applyTo(&svc)

		if !validateAndSnapService(w, r, store, &svc, req.RouteSlug) {
			return
		}

		if err := store.CreateUserService(r.Context(), svc); err != nil {
			writeInternalError(r.Context(), w, "creating service", err)
			return
		}

		// Re-read so the response carries the database-assigned timestamps
		// rather than the zero values on the struct we just wrote.
		if stored, found, err := store.GetUserServiceByID(r.Context(), svc.ID); err == nil && found {
			svc = stored
		}

		w.Header().Set("Location", "/api/services/"+svc.Slug)
		writeJSON(w, http.StatusCreated, withBoardingWait(r.Context(), svc, nil, boardingWait))
	}
}

func GetService(store ServiceStore, boardingWait transit.BoardingWaitPolicy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, ok := loadService(w, r, store)
		if !ok {
			return
		}
		if !authorizeService(w, r, svc) {
			return
		}
		writeJSON(w, http.StatusOK, withBoardingWait(r.Context(), svc, nil, boardingWait))
	}
}

func MyUserServices(store ServiceStore, boardingWait transit.BoardingWaitPolicy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		services, err := store.ListUserServicesByOwner(r.Context(), user.ID)
		if err != nil {
			writeInternalError(r.Context(), w, "listing services", err)
			return
		}
		if services == nil {
			services = []transit.UserService{}
		}
		writeJSON(w, http.StatusOK, withBoardingWaits(r.Context(), services, nil, boardingWait))
	}
}

func UpdateService(store ServiceStore, boardingWait transit.BoardingWaitPolicy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, ok := loadService(w, r, store)
		if !ok {
			return
		}
		if !authorizeService(w, r, svc) {
			return
		}

		req, ok := decodeServiceRequest(w, r)
		if !ok {
			return
		}
		// applyTo touches only client-writable fields, so ID, Slug, and OwnerID
		// carry over from the stored service.
		req.applyTo(&svc)

		if !validateAndSnapService(w, r, store, &svc, req.RouteSlug) {
			return
		}
		if err := store.UpdateUserService(r.Context(), svc); err != nil {
			writeInternalError(r.Context(), w, "updating service", err)
			return
		}

		if stored, found, err := store.GetUserServiceByID(r.Context(), svc.ID); err == nil && found {
			svc = stored
		}
		writeJSON(w, http.StatusOK, withBoardingWait(r.Context(), svc, nil, boardingWait))
	}
}

func DeleteService(store ServiceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, ok := loadService(w, r, store)
		if !ok {
			return
		}
		if !authorizeService(w, r, svc) {
			return
		}
		if err := store.DeleteUserService(r.Context(), svc.ID); err != nil {
			writeInternalError(r.Context(), w, "deleting service", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type serviceBySlugStore interface {
	GetUserServiceBySlug(ctx context.Context, slug string) (transit.UserService, bool, error)
}

func loadService(w http.ResponseWriter, r *http.Request, store serviceBySlugStore) (transit.UserService, bool) {
	svc, found, err := store.GetUserServiceBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeInternalError(r.Context(), w, "loading service", err)
		return transit.UserService{}, false
	}
	if !found {
		writeError(w, http.StatusNotFound, "service not found")
		return transit.UserService{}, false
	}
	return svc, true
}

func authorizeService(w http.ResponseWriter, r *http.Request, svc transit.UserService) bool {
	user, ok := auth.UserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return false
	}
	// OwnerID is NOT NULL for a user service, so the pointer is never nil here;
	// CanAccess takes one to cover the unowned curated rows elsewhere.
	if !auth.CanAccess(user, &svc.OwnerID) {
		writeError(w, http.StatusNotFound, "service not found")
		return false
	}
	return true
}

func decodeServiceRequest(w http.ResponseWriter, r *http.Request) (serviceRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxServiceBodyBytes)

	var req serviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return serviceRequest{}, false
		}
		writeError(w, http.StatusBadRequest, "request body is not valid JSON")
		return serviceRequest{}, false
	}
	if err := req.BoardingWait.parse(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return serviceRequest{}, false
	}
	return req, true
}

const StopPlacementErrorCode = "stop_placement"

type stopPlacementDetail struct {
	Fault      transit.StopPlacementFaultKind `json:"fault"`
	RouteSlug  string                         `json:"route_slug"`
	ThresholdM float64                        `json:"threshold_m,omitempty"`
	Stops      []transit.FaultedStop          `json:"stops"`
}

func stopPlacementDetailFrom(fault *transit.StopPlacementFault) stopPlacementDetail {
	return stopPlacementDetail{
		Fault:      fault.Kind,
		RouteSlug:  fault.RouteSlug,
		ThresholdM: fault.ThresholdM,
		Stops:      fault.Stops,
	}
}

func validateAndSnapService(w http.ResponseWriter, r *http.Request, store ServiceStore, svc *transit.UserService, routeSlug string) bool {
	routeSlug = strings.TrimSpace(routeSlug)
	if routeSlug == "" {
		writeError(w, http.StatusUnprocessableEntity, "route_slug is required")
		return false
	}
	rt, found, err := store.GetRouteBySlug(r.Context(), routeSlug)
	if err != nil {
		writeInternalError(r.Context(), w, "looking up route", err)
		return false
	}
	// CanReference, not CanAccess: a curated route is a public building block
	// any authored service may run over, which is what curated alignments are
	// for. What the check rules out is referencing someone else's *private*
	// draft — reachable before ownership existed only because no route had an
	// owner to check.
	user, _ := auth.UserFrom(r.Context())
	if !found || !auth.CanReference(user, rt.OwnerID) {
		writeError(w, http.StatusUnprocessableEntity, "unknown route_slug "+routeSlug)
		return false
	}
	svc.RouteID = rt.ID

	// Validate first: it range-checks coordinates and the stop count, which
	// snapping would otherwise trip over with a worse message.
	if err := svc.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return false
	}
	if err := svc.SnapToRoute(rt); err != nil {
		// Unusable stored geometry is the server's fault, not the caller's:
		// they submitted a valid service against a route we cannot project on.
		if errors.Is(err, transit.ErrRouteGeometry) {
			writeInternalError(r.Context(), w, "snapping stops", err)
			return false
		}
		var fault *transit.StopPlacementFault
		if errors.As(err, &fault) {
			writeErrorDetail(w, http.StatusUnprocessableEntity,
				StopPlacementErrorCode, fault.Error(), stopPlacementDetailFrom(fault))
			return false
		}
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return false
	}
	return true
}

const maxSlugAttempts = 100

func mintSlug(ctx context.Context, store ServiceStore, name string) (string, error) {
	base := transit.Slugify(name)
	for attempt := 1; attempt <= maxSlugAttempts; attempt++ {
		candidate := base
		if attempt > 1 {
			candidate = fmt.Sprintf("%s-%d", base, attempt)
		}
		_, taken, err := store.GetUserServiceBySlug(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no free slug for %q after %d attempts", base, maxSlugAttempts)
}

func writeInternalError(ctx context.Context, w http.ResponseWriter, op string, err error) {
	trace, _ := traceid.FromContext(ctx)
	slog.ErrorContext(ctx, "handler: internal error", "op", op, "error", err, "trace_id", trace)
	writeError(w, http.StatusInternalServerError, "internal error")
}
