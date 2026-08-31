package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/route"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// OwnedStationStore is the slice of the repository the owner-scoped station
// CRUD needs.
//
// Stations are addressed by (scenario, slug) throughout, because stations.slug
// is unique per scenario rather than globally — there is no station read here
// that is not scenario-scoped, and no global namespace to mint against.
type OwnedStationStore interface {
	ScenarioBySlugStore
	CreateStation(ctx context.Context, st transit.Station) error
	GetStationBySlug(ctx context.Context, scenarioID, slug string) (transit.Station, bool, error)
	UpdateStation(ctx context.Context, st transit.Station) error
	DeleteStation(ctx context.Context, id string) error
	CountStationDependents(ctx context.Context, stationID string) (int, error)
	ListStationsByScenario(ctx context.Context, scenarioID string) ([]transit.Station, error)
}

const maxStationBodyBytes = 1 << 20 // 1 MiB

// stationRequest is the client-writable surface of a station. Identity fields
// (id, scenario_id, slug, owner_id) are absent: the server assigns them all, so
// a client can neither claim an id nor move a station between scenarios.
//
// Coordinates are lat/lng rather than a GeoJSON point, matching every other
// authoring payload in this API (ServiceStopPoint, the isochrone request). The
// stored and read-back shape stays GeoJSON, since that is what the compiler and
// the map consume.
type stationRequest struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lng  float64 `json:"lng"`
	// RoutingLat/RoutingLng optionally move where this station's egress
	// isochrone is centred, without moving the station itself. Both must be
	// given together. See transit.Station.RoutingLocation for why a station
	// would need one: a site still under construction has no walkable network
	// the routing worker can see.
	RoutingLat *float64 `json:"routing_lat"`
	RoutingLng *float64 `json:"routing_lng"`
	// PlatformHeight feeds dwell resolution at compile time, together with the
	// serving vehicle's floor height.
	PlatformHeight string `json:"platform_height"`
}

// applyTo copies the client-writable fields onto st, leaving ID, ScenarioID,
// Slug, and OwnerID untouched.
func (req stationRequest) applyTo(st *transit.Station) {
	st.Name = strings.TrimSpace(req.Name)
	st.Location = transit.GeoPoint{Type: "Point", Coordinates: []float64{req.Lng, req.Lat}}
	st.PlatformHeight = req.PlatformHeight
	if req.RoutingLat != nil && req.RoutingLng != nil {
		st.RoutingLocation = &transit.GeoPoint{
			Type: "Point", Coordinates: []float64{*req.RoutingLng, *req.RoutingLat},
		}
	} else {
		// Clearing it is a legitimate edit: a station whose real location has
		// become routable no longer needs the stand-in.
		st.RoutingLocation = nil
	}
}

// validate rejects what the database would accept but the compiler could not
// use. Coordinate bounds are checked here rather than left to Postgres because
// jsonb will happily store a longitude of 500.
func (req stationRequest) validate() error {
	if strings.TrimSpace(req.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if req.Lat < -90 || req.Lat > 90 {
		return fmt.Errorf("latitude %v is outside [-90, 90]", req.Lat)
	}
	if req.Lng < -180 || req.Lng > 180 {
		return fmt.Errorf("longitude %v is outside [-180, 180]", req.Lng)
	}
	if (req.RoutingLat == nil) != (req.RoutingLng == nil) {
		return fmt.Errorf("routing_lat and routing_lng must be given together")
	}
	if req.RoutingLat != nil {
		if *req.RoutingLat < -90 || *req.RoutingLat > 90 {
			return fmt.Errorf("routing latitude %v is outside [-90, 90]", *req.RoutingLat)
		}
		if *req.RoutingLng < -180 || *req.RoutingLng > 180 {
			return fmt.Errorf("routing longitude %v is outside [-180, 180]", *req.RoutingLng)
		}
	}
	return nil
}

// ListOwnedStations returns the stations of a scenario the caller owns.
func ListOwnedStations(store OwnedStationStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, ok := loadOwnedScenario(w, r, store)
		if !ok {
			return
		}
		stations, err := store.ListStationsByScenario(r.Context(), sc.ID)
		if err != nil {
			writeInternalError(r.Context(), w, "listing stations", err)
			return
		}
		if stations == nil {
			stations = []transit.Station{}
		}
		writeJSON(w, http.StatusOK, stations)
	}
}

// CreateOwnedStation adds a station to a scenario the caller owns.
//
// The station inherits the scenario's owner rather than taking one from the
// request: that is the ownership-uniformity invariant, and it is what lets
// every scenario-scoped read stay unfiltered. The scenario is resolved through
// loadScenarioToAuthorIn rather than loadOwnedScenario so a curated parent is
// refused outright — inheriting from one would mint *curated* platform data
// through an /api/me endpoint.
func CreateOwnedStation(store OwnedStationStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, ok := loadScenarioToAuthorIn(w, r, store)
		if !ok {
			return
		}

		req, ok := decodeStationRequest(w, r)
		if !ok {
			return
		}
		if err := req.validate(); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		slug, err := mintStationSlug(r.Context(), store, sc.ID, req.Name)
		if err != nil {
			writeInternalError(r.Context(), w, "minting station slug", err)
			return
		}
		if slug == "" {
			writeError(w, http.StatusUnprocessableEntity,
				"could not derive a slug from the name; give the station a name with letters or digits in it")
			return
		}

		id, err := ids.NewUUID()
		if err != nil {
			writeInternalError(r.Context(), w, "minting station id", err)
			return
		}

		st := transit.Station{ID: id, ScenarioID: sc.ID, OwnerID: sc.OwnerID, Slug: slug}
		req.applyTo(&st)

		if err := store.CreateStation(r.Context(), st); err != nil {
			writeInternalError(r.Context(), w, "creating station", err)
			return
		}

		w.Header().Set("Location", "/api/me/scenarios/"+sc.Slug+"/stations/"+st.Slug)
		writeJSON(w, http.StatusCreated, st)
	}
}

// UpdateOwnedStation rewrites a station in place.
//
// The slug does not move with a rename. Travel-time segments address stations
// by slug, so re-slugging one would silently orphan every segment naming it and
// the scenario would stop compiling.
func UpdateOwnedStation(store OwnedStationStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, ok := loadOwnedStation(w, r, store)
		if !ok {
			return
		}

		req, ok := decodeStationRequest(w, r)
		if !ok {
			return
		}
		if err := req.validate(); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		req.applyTo(&st)

		if err := store.UpdateStation(r.Context(), st); err != nil {
			writeInternalError(r.Context(), w, "updating station", err)
			return
		}
		writeJSON(w, http.StatusOK, st)
	}
}

// DeleteOwnedStation removes a station once no service stops at it.
//
// service_stops.station_id is RESTRICT, so without the pre-check this would
// surface as an opaque 500 rather than telling the caller what is in the way.
func DeleteOwnedStation(store OwnedStationStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, ok := loadOwnedStation(w, r, store)
		if !ok {
			return
		}

		stops, err := store.CountStationDependents(r.Context(), st.ID)
		if err != nil {
			writeInternalError(r.Context(), w, "counting station dependents", err)
			return
		}
		if stops > 0 {
			writeErrorDetail(w, http.StatusConflict, "station_in_use",
				"this station is still served by a service's stopping pattern",
				map[string]int{"service_stops": stops})
			return
		}

		if err := store.DeleteStation(r.Context(), st.ID); err != nil {
			writeInternalError(r.Context(), w, "deleting station", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// loadOwnedStation resolves {slug}/stations/{stationSlug}, applying the
// scenario's ownership rule first: a station is reachable exactly when its
// scenario is, which is the invariant restated as a lookup.
func loadOwnedStation(w http.ResponseWriter, r *http.Request, store OwnedStationStore) (transit.Station, bool) {
	sc, ok := loadOwnedScenario(w, r, store)
	if !ok {
		return transit.Station{}, false
	}

	st, found, err := store.GetStationBySlug(r.Context(), sc.ID, r.PathValue("stationSlug"))
	if err != nil {
		writeInternalError(r.Context(), w, "looking up station", err)
		return transit.Station{}, false
	}
	if !found {
		writeError(w, http.StatusNotFound, "station not found")
		return transit.Station{}, false
	}
	return st, true
}

func decodeStationRequest(w http.ResponseWriter, r *http.Request) (stationRequest, bool) {
	return decodeBody[stationRequest](w, r, maxStationBodyBytes)
}

// mintStationSlug derives a slug from name, appending -2, -3, ... until it
// finds one free *within this scenario* — the scope stations.slug is unique in.
// It returns "" when the name slugifies to nothing.
func mintStationSlug(ctx context.Context, store OwnedStationStore, scenarioID, name string) (string, error) {
	base := route.Slugify(name)
	if base == "" {
		return "", nil
	}
	// The probe closes over scenarioID: this is the one mint site whose
	// namespace is not global, so a name free in this scenario is minted even
	// when another scenario already holds it.
	return mintUniqueSlug(ctx, base, func(ctx context.Context, candidate string) (bool, error) {
		_, taken, err := store.GetStationBySlug(ctx, scenarioID, candidate)
		return taken, err
	})
}
