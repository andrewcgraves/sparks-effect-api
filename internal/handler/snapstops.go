package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/andrewcgraves/sparks-effect-api/internal/physics"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const offRouteThresholdM = transit.OffRouteThresholdM

const maxSnapStopsBodyBytes = 1 << 20

type snapStopsRequest struct {
	Stops []snapStopInput `json:"stops"`
}

type snapStopInput struct {
	ID  string  `json:"id"`
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type snapCoord struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type snappedStopResult struct {
	ID        string    `json:"id,omitempty"`
	Input     snapCoord `json:"input"`
	Snapped   snapCoord `json:"snapped"`
	ChainageM float64   `json:"chainage_m"`
	OffsetM   float64   `json:"offset_m"`
	OffRoute  bool      `json:"off_route"`
}

type snapStopsResponse struct {
	RouteSlug          string              `json:"route_slug"`
	OffRouteThresholdM float64             `json:"off_route_threshold_m"`
	Stops              []snappedStopResult `json:"stops"`
	ChainageOrder      []int               `json:"chainage_order"`
	OrderIsConsistent  bool                `json:"order_is_consistent"`
}

func SnapStops(store RouteStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeSnapStopsRequest(w, r)
		if !ok {
			return
		}

		slug := r.PathValue("slug")
		rt, found, err := store.GetRouteBySlug(r.Context(), slug)
		if err != nil {
			writeInternalError(r.Context(), w, "looking up route", err)
			return
		}
		if !found || !mayReadRoute(r.Context(), rt) {
			writeError(w, http.StatusNotFound, "route not found")
			return
		}

		// A stored route's geometry was validated at ingestion, so a failure
		// here is bad data rather than a bad request — hence 500, not 400.
		line, err := transit.ToPhysicsLine(rt.Geometry)
		if err != nil {
			writeInternalError(r.Context(), w, "route "+slug+" has unusable geometry", err)
			return
		}

		stops := make([]physics.Stop, len(req.Stops))
		for i, s := range req.Stops {
			stops[i] = physics.Stop{ID: s.ID, Location: physics.Point{Lng: s.Lng, Lat: s.Lat}}
		}

		snapped, err := physics.SnapStops(line, stops)
		if err != nil {
			writeInternalError(r.Context(), w, "snapping stops to route "+slug, err)
			return
		}

		writeJSON(w, http.StatusOK, buildSnapStopsResponse(slug, req.Stops, snapped))
	}
}

func buildSnapStopsResponse(slug string, inputs []snapStopInput, snapped []physics.SnappedStop) snapStopsResponse {
	results := make([]snappedStopResult, len(snapped))
	for i, s := range snapped {
		results[i] = snappedStopResult{
			ID:        inputs[i].ID,
			Input:     snapCoord{Lat: inputs[i].Lat, Lng: inputs[i].Lng},
			Snapped:   snapCoord{Lat: s.Point.Lat, Lng: s.Point.Lng},
			ChainageM: s.ChainageM,
			OffsetM:   s.OffsetM,
			OffRoute:  s.OffsetM > offRouteThresholdM,
		}
	}

	chainages := make([]float64, len(snapped))
	for i, s := range snapped {
		chainages[i] = s.ChainageM
	}
	_, _, faulty := transit.FirstChainageOrderFault(chainages)

	return snapStopsResponse{
		RouteSlug:          slug,
		OffRouteThresholdM: offRouteThresholdM,
		Stops:              results,
		ChainageOrder:      chainageOrder(snapped),
		OrderIsConsistent:  !faulty,
	}
}

func chainageOrder(snapped []physics.SnappedStop) []int {
	order := make([]int, len(snapped))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return snapped[order[a]].ChainageM < snapped[order[b]].ChainageM
	})
	return order
}

func decodeSnapStopsRequest(w http.ResponseWriter, r *http.Request) (snapStopsRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSnapStopsBodyBytes)

	var req snapStopsRequest
	dec := json.NewDecoder(r.Body)
	// Unknown fields are rejected rather than ignored, as in route ingestion: a
	// misspelled coordinate key (latitude) would otherwise decode to zero and
	// preview a stop in the Gulf of Guinea as wildly off-route, blaming the
	// user for a typo the server could see.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return snapStopsRequest{}, false
		}
		writeError(w, http.StatusBadRequest, "malformed request body: "+err.Error())
		return snapStopsRequest{}, false
	}

	if err := validateSnapStops(req.Stops); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return snapStopsRequest{}, false
	}
	return req, true
}

func validateSnapStops(stops []snapStopInput) error {
	if len(stops) == 0 {
		return errors.New("at least one stop is required")
	}
	for i, s := range stops {
		if s.Lat < -90 || s.Lat > 90 {
			return fmt.Errorf("stop %d: lat must be between -90 and 90", i)
		}
		if s.Lng < -180 || s.Lng > 180 {
			return fmt.Errorf("stop %d: lng must be between -180 and 180", i)
		}
	}
	return nil
}
