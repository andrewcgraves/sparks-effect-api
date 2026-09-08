package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type OwnedTravelTimesStore interface {
	OwnedScenarioStore
	UpsertTravelTimes(ctx context.Context, tt transit.TravelTimes) error
	GetTravelTimes(ctx context.Context, scenarioSlug string) (transit.TravelTimes, bool, error)
	ListStationsByScenario(ctx context.Context, scenarioID string) ([]transit.Station, error)
	ListRoutesByScenario(ctx context.Context, scenarioID string) ([]transit.Route, error)
}

const maxTravelTimesBodyBytes = 4 << 20

type travelTimesRequest struct {
	Provenance string                `json:"provenance"`
	Source     string                `json:"source"`
	Segments   []travelTimeSegmentIn `json:"segments"`
}

type travelTimeSegmentIn struct {
	From              string `json:"from"`
	To                string `json:"to"`
	RunSeconds        int    `json:"run_seconds"`
	ReverseRunSeconds *int   `json:"reverse_run_seconds"`
	RouteSlug         string `json:"route_slug"`
}

func GetOwnedTravelTimes(store OwnedTravelTimesStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, ok := loadOwnedScenario(w, r, store)
		if !ok {
			return
		}

		tt, found, err := store.GetTravelTimes(r.Context(), sc.Slug)
		if err != nil {
			writeInternalError(r.Context(), w, "reading travel times", err)
			return
		}
		if !found {
			// An empty set rather than a 404: a scenario that has not been given
			// segment times yet has none, which is a state the editor renders,
			// not an error it reports.
			tt = transit.TravelTimes{ScenarioSlug: sc.Slug}
		}
		if tt.Segments == nil {
			tt.Segments = []transit.SegmentTime{}
		}
		writeJSON(w, http.StatusOK, tt)
	}
}

func ReplaceOwnedTravelTimes(store OwnedTravelTimesStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, ok := loadOwnedScenario(w, r, store)
		if !ok {
			return
		}

		req, ok := decodeTravelTimesRequest(w, r)
		if !ok {
			return
		}

		segments, ok := resolveSegments(w, r, store, sc, req.Segments)
		if !ok {
			return
		}

		tt := transit.TravelTimes{
			ScenarioSlug: sc.Slug,
			Provenance:   req.Provenance,
			Source:       req.Source,
			Segments:     segments,
		}
		if err := store.UpsertTravelTimes(r.Context(), tt); err != nil {
			writeInternalError(r.Context(), w, "writing travel times", err)
			return
		}
		writeJSON(w, http.StatusOK, tt)
	}
}

func resolveSegments(
	w http.ResponseWriter, r *http.Request, store OwnedTravelTimesStore,
	sc transit.Scenario, in []travelTimeSegmentIn,
) ([]transit.SegmentTime, bool) {
	stations, err := store.ListStationsByScenario(r.Context(), sc.ID)
	if err != nil {
		writeInternalError(r.Context(), w, "listing stations", err)
		return nil, false
	}
	known := make(map[string]bool, len(stations))
	for _, st := range stations {
		known[st.Slug] = true
	}

	routes, err := store.ListRoutesByScenario(r.Context(), sc.ID)
	if err != nil {
		writeInternalError(r.Context(), w, "listing routes", err)
		return nil, false
	}
	routeIDBySlug := make(map[string]string, len(routes))
	for _, rt := range routes {
		routeIDBySlug[rt.Slug] = rt.ID
	}

	out := make([]transit.SegmentTime, 0, len(in))
	for i, seg := range in {
		where := fmt.Sprintf("segment %d", i)
		if !known[seg.From] {
			writeError(w, http.StatusUnprocessableEntity,
				where+": unknown station slug "+seg.From)
			return nil, false
		}
		if !known[seg.To] {
			writeError(w, http.StatusUnprocessableEntity,
				where+": unknown station slug "+seg.To)
			return nil, false
		}
		if seg.From == seg.To {
			writeError(w, http.StatusUnprocessableEntity,
				where+": from and to are the same station")
			return nil, false
		}
		if seg.RunSeconds <= 0 {
			writeError(w, http.StatusUnprocessableEntity,
				where+": run_seconds must be positive")
			return nil, false
		}
		if seg.ReverseRunSeconds != nil && *seg.ReverseRunSeconds <= 0 {
			writeError(w, http.StatusUnprocessableEntity,
				where+": reverse_run_seconds must be positive when given")
			return nil, false
		}
		routeID, onScenario := routeIDBySlug[seg.RouteSlug]
		if !onScenario {
			// A segment is track of its route, so a route from another scenario
			// (or none at all) would leave the segment describing nothing.
			writeError(w, http.StatusUnprocessableEntity,
				where+": unknown route slug "+seg.RouteSlug+" for this scenario")
			return nil, false
		}

		out = append(out, transit.SegmentTime{
			FromSlug:          seg.From,
			ToSlug:            seg.To,
			RunSeconds:        seg.RunSeconds,
			ReverseRunSeconds: seg.ReverseRunSeconds,
			RouteID:           routeID,
		})
	}
	return out, true
}

func decodeTravelTimesRequest(w http.ResponseWriter, r *http.Request) (travelTimesRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxTravelTimesBodyBytes)

	var req travelTimesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return travelTimesRequest{}, false
		}
		writeError(w, http.StatusBadRequest, "request body is not valid JSON")
		return travelTimesRequest{}, false
	}
	return req, true
}
