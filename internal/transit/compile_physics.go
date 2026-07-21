package transit

import (
	"fmt"
	"math"

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
// It takes a CompilableService rather than any one domain model, so the seeded
// Service and the user-authored UserService compile through the same code —
// see the adapters in compilable.go. svc.Route must have at least 2 geometry
// coordinates and svc.Stops must have at least 2 entries, already ordered.
//
// Vehicle.DwellS is not read here: each stop's dwell is already resolved into
// CompiledStop.DwellS by the adapter, because the two models decide it
// differently.
//
// Edges reuse the same forward motion time in both directions (the existing
// hand-authored-table compiler's convention — see TravelTimes), varying only by
// which end's dwell they carry, matching pathDwellSecs in compile.go.
func CompileServicePhysics(svc CompilableService) (ServiceGraph, error) {
	line, err := toPhysicsLine(svc.Route.Geometry)
	if err != nil {
		return ServiceGraph{}, fmt.Errorf("compile: service %q: %w", svc.ID, err)
	}
	physicsSegs, err := toPhysicsSegments(svc.Route.Segments, len(line))
	if err != nil {
		return ServiceGraph{}, fmt.Errorf("compile: service %q: %w", svc.ID, err)
	}

	// physics.Stop.ID is keyed by stop slug, which is also the graph edge key,
	// so the span results below map straight back onto stops with no
	// intermediate lookup table.
	stopBySlug := make(map[string]CompiledStop, len(svc.Stops))
	physicsStops := make([]physics.Stop, len(svc.Stops))
	for i, stop := range svc.Stops {
		stopBySlug[stop.Slug] = stop
		physicsStops[i] = physics.Stop{ID: stop.Slug, Location: physics.Point{Lng: stop.Lng, Lat: stop.Lat}}
	}

	spans, err := physics.ProjectStops(line, physicsSegs, physicsStops)
	if err != nil {
		return ServiceGraph{}, fmt.Errorf("compile: service %q: %w", svc.ID, err)
	}

	vehicle := physics.VehicleLimits{
		MaxSpeedKMH:     svc.Vehicle.MaxSpeedKMH,
		AccelerationMS2: svc.Vehicle.AccelerationMS2,
		DecelerationMS2: svc.Vehicle.DecelerationMS2,
	}

	sg := ServiceGraph{ServiceID: svc.ID, WaitSecs: bestHeadwayOver2(svc.Windows)}
	for _, span := range spans {
		runSecsF, err := physics.SpanRunSeconds(span, vehicle)
		if err != nil {
			return ServiceGraph{}, fmt.Errorf("compile: service %q span %s->%s: %w",
				svc.ID, span.FromStopID, span.ToStopID, err)
		}
		runSecs := int(math.Round(runSecsF))

		from, to := stopBySlug[span.FromStopID], stopBySlug[span.ToStopID]

		sg.Edges = append(sg.Edges,
			Edge{FromSlug: from.Slug, ToSlug: to.Slug, Seconds: runSecs + to.DwellS},
			Edge{FromSlug: to.Slug, ToSlug: from.Slug, Seconds: runSecs + from.DwellS},
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
