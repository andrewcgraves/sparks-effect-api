package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/traceid"
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
	// GetRouteBySlug resolves the route a service is authored against. The
	// whole route is needed, not just its existence: stops are snapped onto its
	// geometry before the service is stored.
	GetRouteBySlug(ctx context.Context, slug string) (transit.Route, bool, error)
}

// maxServiceBodyBytes caps a request body. A service with a few hundred stops
// stays well under this; anything larger is a client bug or an attack.
const maxServiceBodyBytes = 1 << 20 // 1 MiB

// serviceRequest is the client-writable surface of a service. Identity fields
// (id, slug, owner_id) are deliberately absent: the server assigns them, so a
// client cannot claim an ID or reassign ownership by including them.
//
// The route is named by slug rather than by ID, matching route ingestion's
// scenario_slug and the route picker in GET /api/routes: a client addresses
// routes by slug everywhere else, and resolving it server-side means a client
// can never supply an arbitrary route_id directly.
type serviceRequest struct {
	RouteSlug        string                     `json:"route_slug"`
	Name             string                     `json:"name"`
	Description      string                     `json:"description"`
	Vehicle          transit.VehicleParams      `json:"vehicle"`
	Stops            []transit.ServiceStopPoint `json:"stops"`
	FrequencyWindows []transit.FrequencyWindow  `json:"frequency_windows"`
	BoardingWait     optionalBoardingWait       `json:"boarding_wait"`
}

// applyTo copies the client-writable fields onto svc, leaving ID, Slug, and
// OwnerID untouched. RouteID is not among them: it is resolved from the request
// slug by validateAndSnapService, which needs the route itself anyway.
//
// The two server-assigned parts of a stop — its Seq and its Slug — are settled
// here, in the one place a client's stops are taken in. Minting unconditionally
// is what makes the slug unspoofable: serviceRequest.Stops is the model type, so
// a client can put a slug on the wire, and anything short of overwriting every
// one of them would let it name another service's stop.
//
// svc.Slug must already be set, since a stop identity is namespaced by its
// service (see transit.UserService.MintStopSlugs).
//
// BoardingWait is the exception to full-replace: omitted leaves the stored
// override, explicit null clears it to inherit.
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

// CreateService persists a new user-authored service owned by the caller.
//
// The owner is the identity the middleware resolved from the bearer token, so
// there is no field a client can set to create a service in someone else's
// name.
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

// GetService returns one service by slug. Reads are owner-scoped: a service is
// authored content, not curated platform data, so it is visible only to its
// owner (and to admins, per auth.CanAccess).
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

// MyUserServices returns the user-authored services owned by the caller.
//
// Distinct from MyServices, which lists the seeded transit.Service rows the
// physics compiler consumes; these are the self-contained services a user
// authored themselves.
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

// UpdateService replaces a service the caller owns.
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
			writeInternalError(r.Context(), w, "deleting service", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// serviceBySlugStore is the single lookup loadService needs, narrower than
// ServiceStore so the compile, graph, and isochrone surfaces can resolve a
// service through the same loader without carrying the whole CRUD slice.
type serviceBySlugStore interface {
	GetUserServiceBySlug(ctx context.Context, slug string) (transit.UserService, bool, error)
}

// loadService resolves the {slug} path value, writing 404 or 500 and reporting
// false when it cannot.
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
	req, ok := decodeBody[serviceRequest](w, r, maxServiceBodyBytes)
	if !ok {
		return serviceRequest{}, false
	}
	if err := req.BoardingWait.parse(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return serviceRequest{}, false
	}
	return req, true
}

// StopPlacementErrorCode marks a 422 whose detail is a stopPlacementDetail: the
// submitted stops broke a placement rule, and the response names which ones.
//
// A client keys per-stop feedback off this rather than off the message text.
// SPA-146 recovered stop names from that prose with regular expressions, so a
// rewording silently cost the authoring UI its stop-row highlighting; the code
// and detail exist so wording is free to change.
const StopPlacementErrorCode = "stop_placement"

// stopPlacementDetail is the wire form of transit.StopPlacementFault.
//
// It exists rather than the fault being marshalled directly because one field
// is deliberately withheld: Backwards decides only how the message reads, and
// publishing it would invite a client to render a distinction that is not a
// fault at all — a service running against its route's drawn direction is
// perfectly ordinary. The stops need no such filtering, so they are the transit
// type itself rather than a handler clone of the same five fields.
type stopPlacementDetail struct {
	// Fault is the kind, "off_route" or "chainage_order". A client that does
	// not recognise the value should fall back to displaying the message.
	Fault     transit.StopPlacementFaultKind `json:"fault"`
	RouteSlug string                         `json:"route_slug"`
	// ThresholdM is echoed on an off-route fault so the client draws the same
	// boundary the server enforced, matching the snap preview's
	// off_route_threshold_m. An order fault is not measured against a distance,
	// so it is omitted there rather than reported as a meaningless zero.
	ThresholdM float64               `json:"threshold_m,omitempty"`
	Stops      []transit.FaultedStop `json:"stops"`
}

func stopPlacementDetailFrom(fault *transit.StopPlacementFault) stopPlacementDetail {
	return stopPlacementDetail{
		Fault:      fault.Kind,
		RouteSlug:  fault.RouteSlug,
		ThresholdM: fault.ThresholdM,
		Stops:      fault.Stops,
	}
}

// validateAndSnapService resolves the route the request names, validates the
// service against it, and snaps its stops onto that route's alignment — after
// which svc holds the coordinates that will be stored.
//
// Resolving the route also settles what would otherwise be a foreign-key 500:
// an unknown slug is a 422 the client can act on. Snapping happens here rather
// than in the store because it is a validation step as much as a normalisation
// one: a stop the route does not pass anywhere near, or a sequence that runs
// against the alignment, is refused rather than quietly stored.
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

// mintSlug derives a URL-safe slug from name, appending -2, -3, ... until it
// finds one no service is using. The loop, its bound, and its exhaustion error
// live in mintUniqueSlug; what is local to a service is the namespace probed
// and transit.Slugify's fallback for a name that slugifies to nothing.
func mintSlug(ctx context.Context, store ServiceStore, name string) (string, error) {
	return mintUniqueSlug(ctx, transit.Slugify(name), func(ctx context.Context, candidate string) (bool, error) {
		_, taken, err := store.GetUserServiceBySlug(ctx, candidate)
		return taken, err
	})
}

// writeInternalError logs the underlying cause and returns an opaque 500, so
// database details never reach the client.
//
// It takes ctx (every call site is inside an HTTP handler, so r.Context() is
// always in scope) so the log line carries the request's trace id — the one
// most worth correlating when a request fails with a 500.
func writeInternalError(ctx context.Context, w http.ResponseWriter, op string, err error) {
	trace, _ := traceid.FromContext(ctx)
	slog.ErrorContext(ctx, "handler: internal error", "op", op, "error", err, "trace_id", trace)
	writeError(w, http.StatusInternalServerError, "internal error")
}
