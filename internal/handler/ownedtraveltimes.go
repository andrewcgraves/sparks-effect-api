package handler

import (
	"context"
	"fmt"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// OwnedTravelTimesStore is the slice of the repository the owner-scoped
// travel-time surface needs.
type OwnedTravelTimesStore interface {
	ScenarioBySlugStore
	UpsertTravelTimes(ctx context.Context, tt transit.TravelTimes) error
	GetTravelTimes(ctx context.Context, scenarioSlug string) (transit.TravelTimes, bool, error)
	ListStationsByScenario(ctx context.Context, scenarioID string) ([]transit.Station, error)
	ListRoutesByScenario(ctx context.Context, scenarioID string) ([]transit.Route, error)
}

const maxTravelTimesBodyBytes = 4 << 20 // 4 MiB

// travelTimesRequest is the client-writable surface of a scenario's segment run
// times. The scenario is named by the path, not the body, so a client cannot
// write one scenario's times into another.
//
// Segments carry from/to station slugs and a route slug rather than ids, for
// the reason every other authoring payload names things by slug: a client
// addresses stations and routes by slug everywhere else, and resolving here
// means it can never supply an arbitrary id.
//
// provenance and source are deliberately absent, for the reason they are absent
// from ownedServiceRequest: they are editorial claims about where a set of
// numbers came from — calibrated against a published timetable, frozen from an
// import — and a user cannot make such a claim about their own work. They stay
// empty on an owned set, which is the honest answer for times somebody authored
// by hand.
type travelTimesRequest struct {
	Segments []travelTimeSegmentIn `json:"segments"`
}

type travelTimeSegmentIn struct {
	From string `json:"from"`
	To   string `json:"to"`
	// RunSeconds is run time only — the train in motion. Dwell is resolved
	// separately at compile time from vehicle and platform height, so counting
	// it here would double-count it.
	RunSeconds int `json:"run_seconds"`
	// ReverseRunSeconds, when omitted, means the reverse direction reuses
	// RunSeconds. It exists for genuinely asymmetric hops (SPA-245).
	ReverseRunSeconds *int   `json:"reverse_run_seconds"`
	RouteSlug         string `json:"route_slug"`
}

// GetOwnedTravelTimes returns the segment run times of a scenario the caller
// owns.
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

// ReplaceOwnedTravelTimes rewrites a scenario's whole segment set.
//
// It is a PUT of the entire collection rather than per-segment CRUD because a
// segment has no identity a client can address — segments carry a generated row
// id nothing references — and because the set is meaningful only as a whole:
// the compiler walks it as one graph, and a half-applied edit is a scenario
// that no longer compiles.
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

		// Provenance and Source are left empty rather than taken from the
		// caller: they are the editorial claim ownedServiceRequest refuses for
		// the same reason, and an owner authoring their own run times is not in
		// a position to make it.
		tt := transit.TravelTimes{
			ScenarioSlug: sc.Slug,
			Segments:     segments,
		}
		if err := store.UpsertTravelTimes(r.Context(), tt); err != nil {
			writeInternalError(r.Context(), w, "writing travel times", err)
			return
		}
		writeJSON(w, http.StatusOK, tt)
	}
}

// resolveSegments turns the wire form into domain segments, checking every
// reference as it goes.
//
// Each check is the difference between a 422 naming the offending slug and a
// scenario that stores fine and then fails to compile with a message about a
// stop "not on any segment path" — which is far harder to act on. Both station
// slugs must belong to this scenario, and the route must be one of its own.
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
	return decodeBody[travelTimesRequest](w, r, maxTravelTimesBodyBytes)
}
