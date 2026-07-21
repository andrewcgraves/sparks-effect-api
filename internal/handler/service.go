package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// ServiceStore is the slice of the repository the user-service CRUD handlers
// need. It is narrower than transit.Repository so these handlers can be tested
// against a small fake.
type ServiceStore interface {
	CreateUserService(ctx context.Context, svc transit.UserService) error
	GetUserServiceByID(ctx context.Context, id string) (transit.UserService, bool, error)
	GetUserServiceBySlug(ctx context.Context, slug string) (transit.UserService, bool, error)
	ListUserServicesByOwner(ctx context.Context, ownerID string) ([]transit.UserService, error)
	UpdateUserService(ctx context.Context, svc transit.UserService) error
	DeleteUserService(ctx context.Context, id string) error
	RouteExists(ctx context.Context, routeID string) (bool, error)
}

// maxServiceBodyBytes caps a request body. A service with a few hundred stops
// stays well under this; anything larger is a client bug or an attack.
const maxServiceBodyBytes = 1 << 20 // 1 MiB

// serviceRequest is the client-writable surface of a service. Identity fields
// (id, slug, owner_id) are deliberately absent: the server assigns them, so a
// client cannot claim an ID or reassign ownership by including them.
type serviceRequest struct {
	RouteID          string                     `json:"route_id"`
	Name             string                     `json:"name"`
	Description      string                     `json:"description"`
	Vehicle          transit.VehicleParams      `json:"vehicle"`
	Stops            []transit.ServiceStopPoint `json:"stops"`
	FrequencyWindows []transit.FrequencyWindow  `json:"frequency_windows"`
}

// applyTo copies the client-writable fields onto svc, leaving ID, Slug, and
// OwnerID untouched.
func (req serviceRequest) applyTo(svc *transit.UserService) {
	svc.RouteID = req.RouteID
	svc.Name = req.Name
	svc.Description = req.Description
	svc.Vehicle = req.Vehicle
	svc.Stops = req.Stops
	svc.FrequencyWindows = req.FrequencyWindows
	svc.NormalizeStops()
}

// CreateService persists a new user-authored service owned by the caller.
//
// The owner is the identity the middleware resolved from the bearer token, so
// there is no field a client can set to create a service in someone else's
// name.
func CreateService(store ServiceStore) http.HandlerFunc {
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
			writeInternalError(w, "minting service id", err)
			return
		}

		svc := transit.UserService{ID: id, OwnerID: user.ID}
		req.applyTo(&svc)

		if !validateService(w, r, store, svc) {
			return
		}

		slug, err := mintSlug(r.Context(), store, svc.Name)
		if err != nil {
			writeInternalError(w, "minting slug", err)
			return
		}
		svc.Slug = slug

		if err := store.CreateUserService(r.Context(), svc); err != nil {
			writeInternalError(w, "creating service", err)
			return
		}

		// Re-read so the response carries the database-assigned timestamps
		// rather than the zero values on the struct we just wrote.
		if stored, found, err := store.GetUserServiceByID(r.Context(), svc.ID); err == nil && found {
			svc = stored
		}

		w.Header().Set("Location", "/api/services/"+svc.Slug)
		writeJSON(w, http.StatusCreated, svc)
	}
}

// GetService returns one service by slug. Reads are owner-scoped: a service is
// authored content, not curated platform data, so it is visible only to its
// owner (and to admins, per auth.CanAccess).
func GetService(store ServiceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, ok := loadService(w, r, store)
		if !ok {
			return
		}
		if !authorizeService(w, r, svc) {
			return
		}
		writeJSON(w, http.StatusOK, svc)
	}
}

// MyUserServices returns the user-authored services owned by the caller.
//
// Distinct from MyServices, which lists the seeded transit.Service rows the
// physics compiler consumes; these are the self-contained services a user
// authored themselves.
func MyUserServices(store ServiceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		services, err := store.ListUserServicesByOwner(r.Context(), user.ID)
		if err != nil {
			writeInternalError(w, "listing services", err)
			return
		}
		if services == nil {
			services = []transit.UserService{}
		}
		writeJSON(w, http.StatusOK, services)
	}
}

// UpdateService replaces a service the caller owns.
func UpdateService(store ServiceStore) http.HandlerFunc {
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

		if !validateService(w, r, store, svc) {
			return
		}
		if err := store.UpdateUserService(r.Context(), svc); err != nil {
			writeInternalError(w, "updating service", err)
			return
		}

		if stored, found, err := store.GetUserServiceByID(r.Context(), svc.ID); err == nil && found {
			svc = stored
		}
		writeJSON(w, http.StatusOK, svc)
	}
}

// DeleteService removes a service the caller owns.
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
			writeInternalError(w, "deleting service", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// loadService resolves the {slug} path value, writing 404 or 500 and reporting
// false when it cannot.
func loadService(w http.ResponseWriter, r *http.Request, store ServiceStore) (transit.UserService, bool) {
	svc, found, err := store.GetUserServiceBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeInternalError(w, "loading service", err)
		return transit.UserService{}, false
	}
	if !found {
		writeError(w, http.StatusNotFound, "service not found")
		return transit.UserService{}, false
	}
	return svc, true
}

// authorizeService applies the shared ownership rule to a loaded service.
//
// It answers 404 rather than 403 so a non-owner cannot probe which slugs exist;
// unlike the curated data, the set of authored services is not public
// knowledge, and the slug is derived from a user-chosen name.
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
	return req, true
}

// validateService applies domain validation and confirms the target route
// exists, so a bad route_id is a 422 rather than a foreign-key 500.
func validateService(w http.ResponseWriter, r *http.Request, store ServiceStore, svc transit.UserService) bool {
	if err := svc.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return false
	}
	exists, err := store.RouteExists(r.Context(), svc.RouteID)
	if err != nil {
		writeInternalError(w, "checking route", err)
		return false
	}
	if !exists {
		writeError(w, http.StatusUnprocessableEntity, "unknown route_id")
		return false
	}
	return true
}

// maxSlugAttempts bounds the collision-suffix search. Exhausting it means
// something is badly wrong (or a name is absurdly popular); either way the
// caller gets a 500 rather than an unbounded loop.
const maxSlugAttempts = 100

// mintSlug derives a URL-safe slug from name, appending -2, -3, ... until it
// finds one no service is using.
//
// This is check-then-insert, so two concurrent creates of the same name can
// both see a slug free and the loser will fail the UNIQUE constraint with a
// 500. Acceptable at present scale; the constraint is what keeps it correct.
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

// writeInternalError logs the underlying cause and returns an opaque 500, so
// database details never reach the client.
func writeInternalError(w http.ResponseWriter, op string, err error) {
	log.Printf("handler: %s: %v", op, err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
