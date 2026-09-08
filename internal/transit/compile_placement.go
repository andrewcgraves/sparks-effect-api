package transit

import (
	"github.com/andrewcgraves/sparks-effect-api/internal/physics"
)

// routePlacer answers, for one seeded hop, which corridor it runs over and where
// its two endpoint stations sit along that corridor's alignment (SPA-264).
//
// The seeded compiler works from a calibrated run-time table rather than from
// geometry, so unlike CompileServicePhysics it has no alignment in hand and
// projects nothing. It was handed the scenario's routes all along and discarded
// them; this is what it does with them instead. Snapping goes through
// physics.SnapStops — the same projection the authoring path uses — so a seeded
// chainage and an authored one mean the same thing, which is the whole reason a
// single front-end module can slice either.
//
// Every answer is best-effort. A hop this cannot place yields an edge with no
// route and no chainage, which is exactly the shape a graph compiled before this
// feature has, so the degradation is covered downstream with no second code
// path. Nothing here can fail a compile: progress is decoration on a map, and a
// seed correction that moved a station off its alignment must not be able to
// take the isochrone — or the boot that compiles it — down with it.
type routePlacer struct {
	routesByID     map[string]Route
	stationsBySlug map[string]Station
	// segRoutes is each calibrated segment's corridor, keyed by the unordered
	// pair of stations it joins: the table is authored in one direction and the
	// compiler walks hops in both.
	segRoutes map[[2]string]string
	// chainage memoises one snap per route: slug → chainage for every station
	// that lands within OffRouteThresholdM of that route, and absent for every
	// station that does not. An entry present with an empty map means the route
	// was resolved and nothing could be placed on it, which is still an answer
	// and must not be recomputed per hop.
	chainage map[string]map[string]float64
}

// newRoutePlacer indexes the scenario's routes and its segments' corridors ready
// to answer per hop. Two things it decides quietly, both deliberate:
//
// A segment row whose RouteID is empty is stored as such and read back by place
// as "no corridor", which is indistinguishable from there being no row at all.
// That is the right answer for both — neither says which alignment the hop runs
// over — and it means the seed data need not be complete for a compile to work.
//
// Two segment rows naming the same pair of stations are last-write-wins. Nothing
// upstream rejects that (buildSegmentAdj does not), and a scenario with two
// corridors between one pair of stations has no single answer to give; taking
// the last is arbitrary but total, which is what this function has to be.
func newRoutePlacer(routes []Route, stationsBySlug map[string]Station, tt TravelTimes) *routePlacer {
	byID := make(map[string]Route, len(routes))
	for _, rt := range routes {
		byID[rt.ID] = rt
	}
	segRoutes := make(map[[2]string]string, len(tt.Segments))
	for _, seg := range tt.Segments {
		segRoutes[stationPairKey(seg.FromSlug, seg.ToSlug)] = seg.RouteID
	}
	return &routePlacer{
		routesByID:     byID,
		stationsBySlug: stationsBySlug,
		segRoutes:      segRoutes,
		chainage:       make(map[string]map[string]float64, len(routes)),
	}
}

// stationPairKey orders a pair of station slugs so a segment authored a→b is
// found when a hop is walked b→a.
func stationPairKey(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

// place resolves the corridor a hop runs over and the chainage of its first and
// last station along it. path is the segment path the hop traverses, endpoints
// included, as segmentPathSeconds returns it.
//
// It reports false — meaning "emit an edge with no route" — when the hop's
// segments disagree about the corridor, when any of them names none, when the
// route is unknown or has unusable geometry, or when either endpoint sits
// further than OffRouteThresholdM from the alignment.
//
// Disagreeing segments are refused rather than resolved to one of them: a hop
// running non-stop across two corridors that meet at a shared station has no
// single alignment to measure against, and picking either would draw a stub down
// a corridor the leg only half runs over.
func (p *routePlacer) place(path []string) (routeID string, fromChainageM, toChainageM float64, ok bool) {
	if len(path) < 2 {
		return "", 0, 0, false
	}
	for i := 0; i+1 < len(path); i++ {
		id := p.segRoutes[stationPairKey(path[i], path[i+1])]
		if id == "" {
			return "", 0, 0, false
		}
		if routeID == "" {
			routeID = id
		} else if routeID != id {
			return "", 0, 0, false
		}
	}

	byStation := p.chainagesOn(routeID)
	from, placed := byStation[path[0]]
	if !placed {
		return "", 0, 0, false
	}
	to, placed := byStation[path[len(path)-1]]
	if !placed {
		return "", 0, 0, false
	}
	return routeID, from, to, true
}

// chainagesOn snaps every station of the scenario onto one route, once, and
// keeps the result. A station further than OffRouteThresholdM from the alignment
// is left out rather than recorded at its clamped position: a stub drawn from a
// guessed place on the wrong corridor is worse than no stub.
//
// Every station is snapped rather than only those some hop names, because the
// alternative is one projection pass per hop over a twenty-thousand-vertex
// alignment. Station counts here are small — a seeded scenario is a corridor,
// not a network.
func (p *routePlacer) chainagesOn(routeID string) map[string]float64 {
	if cached, done := p.chainage[routeID]; done {
		return cached
	}
	byStation := map[string]float64{}
	p.chainage[routeID] = byStation

	rt, known := p.routesByID[routeID]
	if !known {
		return byStation
	}
	line, err := ToPhysicsLine(rt.Geometry)
	if err != nil {
		return byStation
	}

	stops := make([]physics.Stop, 0, len(p.stationsBySlug))
	for slug, st := range p.stationsBySlug {
		if len(st.Location.Coordinates) < 2 {
			continue
		}
		stops = append(stops, physics.Stop{
			ID:       slug,
			Location: physics.Point{Lng: st.Location.Coordinates[0], Lat: st.Location.Coordinates[1]},
		})
	}
	if len(stops) == 0 {
		return byStation
	}
	snapped, err := physics.SnapStops(line, stops)
	if err != nil {
		return byStation
	}
	for _, s := range snapped {
		if s.OffsetM <= OffRouteThresholdM {
			byStation[s.ID] = s.ChainageM
		}
	}
	return byStation
}
