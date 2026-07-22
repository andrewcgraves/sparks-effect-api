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

// offRouteThresholdM is how far a stop may sit from an alignment before the
// preview flags it as implausible.
//
// Nothing rejects at this distance yet — write-time enforcement is SPA-108,
// and when it lands it belongs in this package too, so it should read this
// constant rather than declare its own. A preview that warned at a different
// distance from the one the save enforced would be worse than no warning, since
// the user would fix what it complained about and still be refused.
//
// The comparison is strict (offset > threshold), so a stop exactly on the
// boundary previews as acceptable; enforcement must draw the line the same way.
const offRouteThresholdM = 500.0

// maxSnapStopsBodyBytes caps a request body. A pattern of a few hundred stops
// stays well under this; anything larger is a client bug or an attack.
const maxSnapStopsBodyBytes = 1 << 20 // 1 MiB

// snapStopsRequest is a list of raw, user-placed points to project onto a
// route. It carries no service or vehicle context: this is a geometry preview,
// not a draft of anything that gets persisted.
type snapStopsRequest struct {
	Stops []snapStopInput `json:"stops"`
}

// snapStopInput is one raw point. ID is optional and opaque — the client's own
// handle for the stop, echoed back so it can match results to the row it is
// editing without counting. Results are in input order regardless, so an
// absent or repeated ID costs nothing.
type snapStopInput struct {
	ID  string  `json:"id"`
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type snapCoord struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// snappedStopResult is one stop's projection: where it landed, how far along
// the route that is, and how far it moved to get there.
//
// There is no index field: results are in input order, so a stop's position in
// the array is its index, and that is what chainage_order refers to.
type snappedStopResult struct {
	ID        string    `json:"id,omitempty"`
	Input     snapCoord `json:"input"`
	Snapped   snapCoord `json:"snapped"`
	ChainageM float64   `json:"chainage_m"`
	OffsetM   float64   `json:"offset_m"`
	OffRoute  bool      `json:"off_route"`
}

// snapStopsResponse reports the snap in input order, alongside the order the
// stops actually fall in along the line.
//
// The two orders are reported separately rather than the response being sorted:
// a client that got back a reordered list could not tell a reordering from its
// own mistake, and the disagreement is precisely the thing worth showing the
// user. OffRouteThresholdM is echoed so the client renders the same boundary
// the server applied instead of hard-coding a copy of it.
type snapStopsResponse struct {
	RouteSlug          string              `json:"route_slug"`
	OffRouteThresholdM float64             `json:"off_route_threshold_m"`
	Stops              []snappedStopResult `json:"stops"`
	ChainageOrder      []int               `json:"chainage_order"`
	OrderMatchesInput  bool                `json:"order_matches_input"`
}

// SnapStops previews where a set of raw, user-placed points land on a route:
// the snapped coordinate, its chainage along the alignment, and how far the
// input sat from the line.
//
// It always answers 200 for a well-formed request, flagging stops beyond
// offRouteThresholdM rather than refusing them. Rejecting an off-route stop is
// the write path's job; this endpoint exists so the user sees the problem while
// they can still drag the marker, and an endpoint that refused to answer could
// not show them what was wrong.
//
// It is public, like the route read it previews against — the alignment it
// projects onto is already readable by anyone, and the snap adds no information
// that geometry does not already contain.
func SnapStops(store RouteStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeSnapStopsRequest(w, r)
		if !ok {
			return
		}

		slug := r.PathValue("slug")
		rt, found, err := store.GetRouteBySlug(r.Context(), slug)
		if err != nil {
			writeInternalError(w, "looking up route", err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "route not found")
			return
		}

		// A stored route's geometry was validated at ingestion, so a failure
		// here is bad data rather than a bad request — hence 500, not 400.
		line, err := transit.ToPhysicsLine(rt.Geometry)
		if err != nil {
			writeInternalError(w, "route "+slug+" has unusable geometry", err)
			return
		}

		stops := make([]physics.Stop, len(req.Stops))
		for i, s := range req.Stops {
			stops[i] = physics.Stop{ID: s.ID, Location: physics.Point{Lng: s.Lng, Lat: s.Lat}}
		}

		snapped, err := physics.SnapStops(line, stops)
		if err != nil {
			writeInternalError(w, "snapping stops to route "+slug, err)
			return
		}

		writeJSON(w, http.StatusOK, buildSnapStopsResponse(slug, req.Stops, snapped))
	}
}

// buildSnapStopsResponse pairs each raw input with its projection. SnapStops
// preserves input order, so the two slices are index-aligned.
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

	order, matches := chainageOrder(snapped)
	return snapStopsResponse{
		RouteSlug:          slug,
		OffRouteThresholdM: offRouteThresholdM,
		Stops:              results,
		ChainageOrder:      order,
		OrderMatchesInput:  matches,
	}
}

// chainageOrder returns the input indices sorted by distance along the route,
// and whether that agrees with the order they were supplied in. The sort is
// stable so two stops at the same chainage keep their input order and are
// reported as agreeing rather than as an arbitrary reshuffle.
func chainageOrder(snapped []physics.SnappedStop) (order []int, matchesInput bool) {
	order = make([]int, len(snapped))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return snapped[order[a]].ChainageM < snapped[order[b]].ChainageM
	})

	for i, idx := range order {
		if idx != i {
			return order, false
		}
	}
	return order, true
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

// validateSnapStops checks the only thing the endpoint can check without the
// route: that there is at least one stop and each is a real coordinate. Unlike
// a service, a preview has no minimum of two — the authoring UI snaps each stop
// as it is placed.
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
