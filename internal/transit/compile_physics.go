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
// coordinates, and svc.Stops must have at least 2 entries, already ordered and
// with distinct slugs.
//
// An inactive seeded service is not special-cased here: whether a service
// belongs in a graph at all is scenario-assembly semantics, and CompileScenario
// already skips it before reaching this.
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

	// physics.Stop.ID is keyed by stop slug, and edges are emitted under it, so
	// the span results below map straight back onto stops with no intermediate
	// lookup table. That makes slug uniqueness load-bearing: two stops sharing
	// one would silently collapse into a single graph node and lose a span, so
	// reject it rather than compile a quietly wrong graph.
	//
	// SPA-109 merges co-located stops onto one node, which is the same collapse
	// this rejects. The two coexist because that clustering is scoped to
	// cross-service pairs only: it may hand two different services' stops one
	// key, but never two stops of a single service, so it cannot be what puts a
	// duplicate in front of this check. A lone service stopping twice within the
	// merge radius is a loop or a switchback, not an interchange, and merging it
	// really would delete a span. Decided on SPA-115 — clustering stays
	// cross-service and this check does not relax to accommodate it.
	stopBySlug := make(map[string]CompilableStop, len(svc.Stops))
	physicsStops := make([]physics.Stop, len(svc.Stops))
	for i, stop := range svc.Stops {
		if _, dup := stopBySlug[stop.Slug]; dup {
			return ServiceGraph{}, fmt.Errorf("compile: service %q has two stops with slug %q", svc.ID, stop.Slug)
		}
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
