package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/route"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type OwnedStationStore interface {
	OwnedScenarioStore
	CreateStation(ctx context.Context, st transit.Station) error
	GetStationBySlug(ctx context.Context, scenarioID, slug string) (transit.Station, bool, error)
	UpdateStation(ctx context.Context, st transit.Station) error
	DeleteStation(ctx context.Context, id string) error
	CountStationDependents(ctx context.Context, stationID string) (int, error)
	ListStationsByScenario(ctx context.Context, scenarioID string) ([]transit.Station, error)
}

const maxStationBodyBytes = 1 << 20

type stationRequest struct {
	Name           string   `json:"name"`
	Lat            float64  `json:"lat"`
	Lng            float64  `json:"lng"`
	RoutingLat     *float64 `json:"routing_lat"`
	RoutingLng     *float64 `json:"routing_lng"`
	PlatformHeight string   `json:"platform_height"`
}

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

func CreateOwnedStation(store OwnedStationStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, ok := loadOwnedScenario(w, r, store)
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
	r.Body = http.MaxBytesReader(w, r.Body, maxStationBodyBytes)

	var req stationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return stationRequest{}, false
		}
		writeError(w, http.StatusBadRequest, "request body is not valid JSON")
		return stationRequest{}, false
	}
	return req, true
}

func mintStationSlug(ctx context.Context, store OwnedStationStore, scenarioID, name string) (string, error) {
	base := route.Slugify(name)
	if base == "" {
		return "", nil
	}
	for attempt := 1; attempt <= maxSlugAttempts; attempt++ {
		candidate := base
		if attempt > 1 {
			candidate = fmt.Sprintf("%s-%d", base, attempt)
		}
		_, taken, err := store.GetStationBySlug(ctx, scenarioID, candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no free slug for %q after %d attempts", base, maxSlugAttempts)
}
