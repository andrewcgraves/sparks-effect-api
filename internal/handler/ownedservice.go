package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// OwnedServiceStore is the slice of the repository the owner-scoped CRUD over
// the seeded service model needs.
//
// A seeded service is addressed by id, not slug: the services table has no slug
// column and transit.Service has no Slug field, which removes slug minting from
// this model entirely.
type OwnedServiceStore interface {
	CreateService(ctx context.Context, svc transit.Service) error
	GetServiceByID(ctx context.Context, id string) (transit.Service, bool, error)
	UpdateService(ctx context.Context, svc transit.Service) error
	DeleteService(ctx context.Context, id string) error
	GetScenarioBySlug(ctx context.Context, slug string) (transit.Scenario, bool, error)
	GetRouteBySlug(ctx context.Context, slug string) (transit.Route, bool, error)
	GetVehicleTypeByID(ctx context.Context, id string) (transit.VehicleType, bool, error)
	ListStationsByScenario(ctx context.Context, scenarioID string) ([]transit.Station, error)
}

const maxOwnedServiceBodyBytes = 1 << 20 // 1 MiB

// ownedServiceRequest is the client-writable surface of a seeded service.
//
// Identity fields (id, owner_id) are absent, as everywhere else. So is
// provenance: computed / calibrated / frozen are editorial claims about where a
// service's numbers came from ("physics-compiled", "imported timetable",
// "locked by policy"), and a user cannot make such a claim about their own
// work. It stays empty on anything authored through this endpoint.
type ownedServiceRequest struct {
	ScenarioSlug  string `json:"scenario_slug"`
	RouteSlug     string `json:"route_slug"`
	VehicleTypeID string `json:"vehicle_type_id"`
	Name          string `json:"name"`
	Direction     string `json:"direction"`
	Active        *bool  `json:"active"`
	// BoardingWait is the exception to full-replace, matching the convention
	// SPA-237 established on the user-service write path: omitted leaves the
	// stored override alone, an explicit null clears it back to inherit, and an
	// object sets it. optionalBoardingWait is what tells those three apart, and
	// its parse() is where an invalid policy becomes a 422.
	BoardingWait     optionalBoardingWait      `json:"boarding_wait"`
	Stops            []ownedServiceStopIn      `json:"stops"`
	FrequencyWindows []transit.FrequencyWindow `json:"frequency_windows"`
}

// ownedServiceStopIn names a station by slug rather than id, so a client can
// never supply an arbitrary station_id — and in particular not one belonging to
// somebody else's scenario.
type ownedServiceStopIn struct {
	StationSlug string `json:"station_slug"`
	Sequence    int    `json:"sequence"`
	// DwellS overrides the dwell the compiler would otherwise resolve from
	// vehicle floor height against platform height. Omitted means "resolve it".
	DwellS *int `json:"dwell_s"`
}

// CreateOwnedService persists a service inside a scenario the caller owns.
func CreateOwnedService(store OwnedServiceStore, boardingWait transit.BoardingWaitPolicy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		req, ok := decodeOwnedServiceRequest(w, r)
		if !ok {
			return
		}

		id, err := ids.NewUUID()
		if err != nil {
			writeInternalError(r.Context(), w, "minting service id", err)
			return
		}

		svc := transit.Service{ID: id, OwnerID: &user.ID}
		if !resolveOwnedService(w, r, store, user, req, &svc) {
			return
		}

		// Deliberately no AddServiceToScenario call. That join is the scenario's
		// *curated* membership — what the public reads and the prerendered
		// isochrones' staleness check are computed from. An owned service
		// entering it would flip every curated entry to outdated. The service
		// still compiles: the compiler reads services by scenario_id, not
		// through the join.
		if err := store.CreateService(r.Context(), svc); err != nil {
			writeInternalError(r.Context(), w, "creating service", err)
			return
		}

		w.Header().Set("Location", "/api/me/services/"+svc.ID)
		writeJSON(w, http.StatusCreated,
			withBoardingWait[transit.Service](r.Context(), svc, nil, boardingWait))
	}
}

// GetOwnedService returns one of the caller's own services, whole.
func GetOwnedService(store OwnedServiceStore, boardingWait transit.BoardingWaitPolicy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, ok := loadOwnedService(w, r, store)
		if !ok {
			return
		}
		// The same response-only fill MyServices does for the list read, so a
		// client never re-derives min(headway)/2 itself.
		//
		// The scenario argument is nil because SPA-237 put the override columns
		// on user_scenarios and services, not on scenarios: a seeded service has
		// no scenario-level override to inherit, so its precedence chain is its
		// own override, then global.
		writeJSON(w, http.StatusOK,
			withBoardingWait[transit.Service](r.Context(), svc, nil, boardingWait))
	}
}

// UpdateOwnedService rewrites a service the caller owns — its scalars, its
// stopping pattern, and its frequency windows.
func UpdateOwnedService(store OwnedServiceStore, boardingWait transit.BoardingWaitPolicy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, ok := loadOwnedService(w, r, store)
		if !ok {
			return
		}
		user, _ := auth.UserFrom(r.Context())

		req, ok := decodeOwnedServiceRequest(w, r)
		if !ok {
			return
		}
		// resolveOwnedService rewrites every client-writable field on the
		// loaded service, so ID and OwnerID carry over and neither can be
		// reassigned through an update.
		if !resolveOwnedService(w, r, store, user, req, &svc) {
			return
		}

		if err := store.UpdateService(r.Context(), svc); err != nil {
			writeInternalError(r.Context(), w, "updating service", err)
			return
		}
		writeJSON(w, http.StatusOK,
			withBoardingWait[transit.Service](r.Context(), svc, nil, boardingWait))
	}
}

// DeleteOwnedService removes a service the caller owns. Its stops and frequency
// windows cascade, and jobs.compiled_service_ids is a bare uuid[] snapshot
// rather than an FK, so a graph compiled earlier correctly keeps listing it.
func DeleteOwnedService(store OwnedServiceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, ok := loadOwnedService(w, r, store)
		if !ok {
			return
		}
		if err := store.DeleteService(r.Context(), svc.ID); err != nil {
			writeInternalError(r.Context(), w, "deleting service", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// resolveOwnedService validates every reference the payload names and writes
// the resolved result onto svc.
//
// services has three NOT NULL foreign keys, so an unchecked reference is a 500
// where a 422 naming the offending slug belongs. Each of the four checks below
// is one of those, plus the ownership rules that keep the uniformity invariant
// intact.
func resolveOwnedService(
	w http.ResponseWriter, r *http.Request, store OwnedServiceStore,
	user transit.User, req ownedServiceRequest, svc *transit.Service,
) bool {
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusUnprocessableEntity, "name is required")
		return false
	}

	// 1. The scenario must be one the caller owns — a service is a child, and a
	//    child shares its parent's owner. CanAccess, not CanReference: adding a
	//    service to a scenario is mutating it, so a curated scenario stays
	//    admin-only.
	sc, found, err := store.GetScenarioBySlug(r.Context(), req.ScenarioSlug)
	if err != nil {
		writeInternalError(r.Context(), w, "looking up scenario", err)
		return false
	}
	if !found || !auth.CanAccess(user, sc.OwnerID) {
		writeError(w, http.StatusUnprocessableEntity, "unknown scenario_slug "+req.ScenarioSlug)
		return false
	}

	// 2. The route may be curated — a public alignment is a building block
	//    anyone may run a service over — but not someone else's private draft.
	rt, found, err := store.GetRouteBySlug(r.Context(), req.RouteSlug)
	if err != nil {
		writeInternalError(r.Context(), w, "looking up route", err)
		return false
	}
	if !found || !auth.CanReference(user, rt.OwnerID) {
		writeError(w, http.StatusUnprocessableEntity, "unknown route_slug "+req.RouteSlug)
		return false
	}

	// 3. Vehicle types are a global, unowned catalog, so any authenticated
	//    caller may reference one; it only has to exist.
	if _, found, err := store.GetVehicleTypeByID(r.Context(), req.VehicleTypeID); err != nil {
		writeInternalError(r.Context(), w, "looking up vehicle type", err)
		return false
	} else if !found {
		writeError(w, http.StatusUnprocessableEntity, "unknown vehicle_type_id "+req.VehicleTypeID)
		return false
	}

	// 4. Every stop must name a station of *this* scenario. Resolving through
	//    the scenario's own stations is what makes borrowing another scenario's
	//    station id impossible rather than merely discouraged.
	stops, ok := resolveServiceStops(w, r, store, sc, req.Stops)
	if !ok {
		return false
	}

	active := true
	if req.Active != nil {
		active = *req.Active
	}

	svc.ScenarioID = sc.ID
	svc.RouteID = rt.ID
	svc.VehicleTypeID = req.VehicleTypeID
	svc.Name = strings.TrimSpace(req.Name)
	svc.Direction = req.Direction
	svc.Active = active
	svc.Provenance = ""
	svc.Stops = stops
	svc.FrequencyWindows = req.FrequencyWindows
	// Only when the client said something about it: an update that omits the
	// key must not silently clear an override the service already carries.
	if req.BoardingWait.set {
		svc.BoardingWait = req.BoardingWait.value
	}
	return true
}

// resolveServiceStops turns station slugs into ids and normalises the pattern.
func resolveServiceStops(
	w http.ResponseWriter, r *http.Request, store OwnedServiceStore,
	sc transit.Scenario, in []ownedServiceStopIn,
) ([]transit.ServiceStop, bool) {
	if len(in) < 2 {
		writeError(w, http.StatusUnprocessableEntity, "a service needs at least two stops")
		return nil, false
	}

	stations, err := store.ListStationsByScenario(r.Context(), sc.ID)
	if err != nil {
		writeInternalError(r.Context(), w, "listing stations", err)
		return nil, false
	}
	idBySlug := make(map[string]string, len(stations))
	for _, st := range stations {
		idBySlug[st.Slug] = st.ID
	}

	seen := make(map[string]bool, len(in))
	out := make([]transit.ServiceStop, 0, len(in))
	for i, stop := range in {
		id, onScenario := idBySlug[stop.StationSlug]
		if !onScenario {
			writeError(w, http.StatusUnprocessableEntity,
				fmt.Sprintf("stop %d: unknown station slug %s for this scenario", i, stop.StationSlug))
			return nil, false
		}
		// service_stops is keyed on (service_id, sequence), so a repeated
		// station would also be a repeated row for the same physical place.
		if seen[stop.StationSlug] {
			writeError(w, http.StatusUnprocessableEntity,
				fmt.Sprintf("stop %d: station %s appears twice", i, stop.StationSlug))
			return nil, false
		}
		seen[stop.StationSlug] = true

		if stop.DwellS != nil && *stop.DwellS < 0 {
			writeError(w, http.StatusUnprocessableEntity,
				fmt.Sprintf("stop %d: dwell_s cannot be negative", i))
			return nil, false
		}
		out = append(out, transit.ServiceStop{
			StationID: id, Sequence: stop.Sequence, DwellS: stop.DwellS,
		})
	}

	// Renumbered from the caller's ordering rather than trusted: the PK is
	// (service_id, sequence), so duplicate or sparse numbers from a client
	// would be a constraint violation or a gap the compiler reads as an order
	// it did not intend.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	for i := range out {
		out[i].Sequence = i + 1
	}
	return out, true
}

// loadOwnedService resolves the {id} path value and applies the ownership rule,
// answering 404 rather than 403 for loadOwnedRoute's reason. A curated service
// lands here too — its owner is nil, so CanAccess admits only admins.
func loadOwnedService(w http.ResponseWriter, r *http.Request, store OwnedServiceStore) (transit.Service, bool) {
	user, ok := auth.UserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return transit.Service{}, false
	}

	svc, found, err := store.GetServiceByID(r.Context(), r.PathValue("id"))
	if err != nil {
		writeInternalError(r.Context(), w, "looking up service", err)
		return transit.Service{}, false
	}
	if !found || !auth.CanAccess(user, svc.OwnerID) {
		writeError(w, http.StatusNotFound, "service not found")
		return transit.Service{}, false
	}
	return svc, true
}

func decodeOwnedServiceRequest(w http.ResponseWriter, r *http.Request) (ownedServiceRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxOwnedServiceBodyBytes)

	var req ownedServiceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return ownedServiceRequest{}, false
		}
		writeError(w, http.StatusBadRequest, "request body is not valid JSON")
		return ownedServiceRequest{}, false
	}
	// Deferred out of UnmarshalJSON so a bad policy is a 422 about the value
	// rather than a 400 about the syntax.
	if err := req.BoardingWait.parse(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return ownedServiceRequest{}, false
	}
	return req, true
}
