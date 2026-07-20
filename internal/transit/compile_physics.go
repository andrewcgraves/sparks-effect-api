package transit

import (
	"fmt"
	"math"
	"sort"

	"github.com/andrewcgraves/sparks-effect-api/internal/physics"
)

// CompileServicePhysics compiles one service's stops on its route directly
// from track geometry and vehicle kinematics — accelerate/cruise/decelerate
// speed-profile integration per inter-stop span, via internal/physics —
// rather than a hand-authored named-segment run-time table. This is the path
// user-generated routes and services use; Compile's segmentRunTimes table
// remains how seeded scenarios (with no per-service route geometry to
// project stops onto) are compiled.
//
// route must have at least 2 geometry coordinates. svc's stops, ordered by
// Sequence, must each reference a station in stations; there must be at
// least 2. Edges reuse the same forward motion time in both directions (the
// existing hand-authored-table compiler's convention — see TravelTimes),
// varying only by which end's dwell they carry, matching pathDwellSecs in
// compile.go.
func CompileServicePhysics(route Route, stations []Station, svc Service, vt VehicleType) (ServiceGraph, error) {
	line, err := toPhysicsLine(route.Geometry)
	if err != nil {
		return ServiceGraph{}, fmt.Errorf("compile: service %q: %w", svc.ID, err)
	}
	physicsSegs, err := toPhysicsSegments(route.Segments, len(line))
	if err != nil {
		return ServiceGraph{}, fmt.Errorf("compile: service %q: %w", svc.ID, err)
	}

	stationsByID := make(map[string]Station, len(stations))
	for _, st := range stations {
		stationsByID[st.ID] = st
	}

	stops := append([]ServiceStop(nil), svc.Stops...)
	sort.Slice(stops, func(i, j int) bool { return stops[i].Sequence < stops[j].Sequence })

	// physics.Stop.ID is keyed by station ID (not slug) so the span results
	// below can look stations and stops back up in stationsByID /
	// stopByStationID directly, with no reverse slug->ID map in between.
	stopByStationID := make(map[string]ServiceStop, len(stops))
	physicsStops := make([]physics.Stop, len(stops))
	for i, stop := range stops {
		st, ok := stationsByID[stop.StationID]
		if !ok {
			return ServiceGraph{}, fmt.Errorf("compile: service %q references unknown station id %q", svc.ID, stop.StationID)
		}
		stopByStationID[stop.StationID] = stop
		physicsStops[i] = physics.Stop{ID: st.ID, Location: toPhysicsPoint(st.Location)}
	}

	spans, err := physics.ProjectStops(line, physicsSegs, physicsStops)
	if err != nil {
		return ServiceGraph{}, fmt.Errorf("compile: service %q: %w", svc.ID, err)
	}

	vehicle := physics.VehicleLimits{
		MaxSpeedKMH:     vt.MaxSpeedKMH,
		AccelerationMS2: vt.AccelerationMS2,
		DecelerationMS2: vt.DecelerationMS2,
	}

	sg := ServiceGraph{ServiceID: svc.ID, WaitSecs: bestHeadwayOver2(svc.FrequencyWindows)}
	for _, span := range spans {
		runSecsF, err := physics.SpanRunSeconds(span, vehicle)
		if err != nil {
			return ServiceGraph{}, fmt.Errorf("compile: service %q span %s->%s: %w",
				svc.ID, span.FromStopID, span.ToStopID, err)
		}
		runSecs := int(math.Round(runSecsF))

		fromStop, toStop := stopByStationID[span.FromStopID], stopByStationID[span.ToStopID]
		fromStation, toStation := stationsByID[span.FromStopID], stationsByID[span.ToStopID]

		sg.Edges = append(sg.Edges,
			Edge{FromSlug: fromStation.Slug, ToSlug: toStation.Slug, Seconds: runSecs + resolveDwell(toStop, toStation, vt)},
			Edge{FromSlug: toStation.Slug, ToSlug: fromStation.Slug, Seconds: runSecs + resolveDwell(fromStop, fromStation, vt)},
		)
	}
	return sg, nil
}

// toPhysicsLine converts a route's GeoJSON LineString to physics.Point
// coordinates, erroring the way physics.ProjectStops itself would (fewer
// than 2 points) so the caller gets one consistent error path.
func toPhysicsLine(g GeoLineString) ([]physics.Point, error) {
	if len(g.Coordinates) < 2 {
		return nil, fmt.Errorf("route geometry must have at least 2 points, got %d", len(g.Coordinates))
	}
	line := make([]physics.Point, len(g.Coordinates))
	for i, c := range g.Coordinates {
		line[i] = physics.Point{Lng: c[0], Lat: c[1]}
	}
	return line, nil
}

// toPhysicsSegments converts a route's per-span track physics to
// physics.Segment. An empty input is passed through as-is: both RouteSegment
// and physics.ProjectStops treat that as tangent, level, uncanted track for
// every span.
func toPhysicsSegments(segs []RouteSegment, lineLen int) ([]physics.Segment, error) {
	if len(segs) == 0 {
		return nil, nil
	}
	if len(segs) != lineLen-1 {
		return nil, fmt.Errorf("route has %d physics segments for %d geometry points, want %d", len(segs), lineLen, lineLen-1)
	}
	out := make([]physics.Segment, len(segs))
	for i, s := range segs {
		out[i] = physics.Segment{CantMM: s.CantMM, CurveRadiusM: s.CurveRadiusM, GradePct: s.GradePct}
	}
	return out, nil
}

func toPhysicsPoint(p GeoPoint) physics.Point {
	return physics.Point{Lng: p.Coordinates[0], Lat: p.Coordinates[1]}
}
