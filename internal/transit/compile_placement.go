package transit

import (
	"github.com/andrewcgraves/sparks-effect-api/internal/physics"
)

type routePlacer struct {
	routesByID     map[string]Route
	stationsBySlug map[string]Station
	segRoutes      map[[2]string]string
	chainage       map[string]map[string]float64
}

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

func stationPairKey(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

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
