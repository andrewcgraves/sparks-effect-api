package physics

import (
	"fmt"
	"math"
	"sort"
)

// earthRadiusM is the mean Earth radius, in meters, used for the local planar
// projection below.
const earthRadiusM = 6371000.0

// Point is a WGS84 geographic position, in GeoJSON coordinate order
// (longitude, then latitude) — the order used throughout this codebase (e.g.
// transit.GeoLineString).
type Point struct {
	Lng float64 // degrees
	Lat float64 // degrees
}

// Segment is the authored track physics for one span between two consecutive
// line coordinates. Its fields mirror transit.RouteSegment; it is defined
// locally so this package stays free of a dependency on internal/transit, the
// same seam internal/route already keeps for its own mirrored Segment type.
// The zero value describes tangent, level, uncanted track.
type Segment struct {
	CantMM       float64 // applied superelevation, millimeters
	CurveRadiusM float64 // curve radius, meters; 0 means tangent (straight) track
	GradePct     float64 // grade as a percent; positive = ascending, negative = descending
}

// Stop is a position to project onto a route line, carrying an opaque
// caller-assigned ID (e.g. a station ID) so results can be traced back to it.
type Stop struct {
	ID       string
	Location Point
}

// SpanSegment is the portion of one underlying route segment that falls
// within an InterStopSpan, together with the physics that applies across it.
// A span whose stops don't land exactly on a line vertex has partial pieces at
// its ends; a span crossing several vertices has one SpanSegment per
// vertex-to-vertex stretch it fully or partly covers.
type SpanSegment struct {
	DistanceM float64
	Physics   Segment
}

// InterStopSpan is the stretch of route line between two consecutive stops,
// ordered by chainage, split into the physics-uniform sub-segments the
// run-time integrator needs to walk. DistanceM is the span's total length —
// the sum of Segments' distances.
type InterStopSpan struct {
	FromStopID string
	ToStopID   string
	DistanceM  float64
	Segments   []SpanSegment
}

// ProjectStops snaps each stop onto the route line, orders the stops by
// chainage (distance along the line from its start), and splits the line's
// geometry and per-vertex-segment physics into the spans between consecutive
// stops.
//
// line is the route's coordinates in order and must have at least 2 points.
// physicsSegs, when non-empty, must have exactly len(line)-1 entries — one per
// span between consecutive line coordinates, the same convention
// transit.Route uses. An empty physicsSegs means every span is tangent, level,
// uncanted track (the zero Segment) — mirroring transit.Route.Segments, which
// is itself optional for the same reason.
//
// Nearest-point snapping assumes line is a simple polyline: on a
// self-intersecting or switchback route, a stop can snap to the geometrically
// closer of two passes rather than the one intended by the service's stopping
// pattern.
//
// ProjectStops returns an error if line has fewer than 2 points, physicsSegs
// is non-empty with the wrong length, or fewer than 2 stops are given (there
// is no inter-stop span with fewer than two).
func ProjectStops(line []Point, physicsSegs []Segment, stops []Stop) ([]InterStopSpan, error) {
	if len(line) < 2 {
		return nil, fmt.Errorf("line must have at least 2 points, got %d", len(line))
	}
	if len(physicsSegs) > 0 && len(physicsSegs) != len(line)-1 {
		return nil, fmt.Errorf("physics must have %d segments for %d line points, got %d",
			len(line)-1, len(line), len(physicsSegs))
	}
	if len(stops) < 2 {
		return nil, fmt.Errorf("need at least 2 stops to form an inter-stop span, got %d", len(stops))
	}

	physics := physicsSegs
	if len(physics) == 0 {
		physics = make([]Segment, len(line)-1)
	}

	pl := projectLinePlanar(line)
	lineSegs := buildLineSegments(pl, physics)

	type projectedStop struct {
		Stop
		chainageM float64
	}
	projected := make([]projectedStop, len(stops))
	for i, s := range stops {
		p := projectPoint(s.Location, pl.refLatRad)
		projected[i] = projectedStop{Stop: s, chainageM: snapToLine(pl, p)}
	}

	sort.SliceStable(projected, func(i, j int) bool {
		return projected[i].chainageM < projected[j].chainageM
	})

	spans := make([]InterStopSpan, 0, len(projected)-1)
	for i := 0; i < len(projected)-1; i++ {
		from, to := projected[i], projected[i+1]
		spans = append(spans, InterStopSpan{
			FromStopID: from.ID,
			ToStopID:   to.ID,
			DistanceM:  to.chainageM - from.chainageM,
			Segments:   splitSpan(lineSegs, from.chainageM, to.chainageM),
		})
	}

	return spans, nil
}

// planarPoint is a position in the local planar (x, y) meter frame produced by
// the equirectangular projection below. It is a distinct type from Point so
// geographic (degree) and projected (meter) coordinates can never be mixed up
// at a call site.
type planarPoint struct {
	X, Y float64
}

// planarLine is a route line projected into local planar meters, alongside
// the per-vertex chainage (distance along the line from its start) and the
// reference latitude the projection used — callers need the latter to project
// stops consistently with the line.
type planarLine struct {
	points    []planarPoint
	chainageM []float64 // chainageM[i] is the distance along the line to points[i]
	refLatRad float64
}

// degToRad converts degrees to radians.
func degToRad(deg float64) float64 {
	return deg * math.Pi / 180
}

// projectLinePlanar converts a WGS84 line to local planar meters using an
// equirectangular approximation around the line's mean latitude, and computes
// each vertex's chainage.
//
// This trades literal geodesic accuracy for a locally-consistent, self-similar
// distance metric — which is what nearest-point projection and chainage need:
// the same answer whether you sum the pieces or measure the whole, not the
// true great-circle length. It is accurate to a small fraction of a percent at
// the route scales this compiler targets (regional rail lines).
func projectLinePlanar(line []Point) planarLine {
	var latSum float64
	for _, p := range line {
		latSum += p.Lat
	}
	refLatRad := degToRad(latSum / float64(len(line)))

	points := make([]planarPoint, len(line))
	for i, p := range line {
		points[i] = projectPoint(p, refLatRad)
	}

	chainageM := make([]float64, len(points))
	for i := 1; i < len(points); i++ {
		chainageM[i] = chainageM[i-1] + planarDist(points[i-1], points[i])
	}

	return planarLine{points: points, chainageM: chainageM, refLatRad: refLatRad}
}

// projectPoint converts a single WGS84 point to local planar meters around
// refLatRad, using the same equirectangular approximation as
// projectLinePlanar.
func projectPoint(p Point, refLatRad float64) planarPoint {
	return planarPoint{
		X: earthRadiusM * degToRad(p.Lng) * math.Cos(refLatRad),
		Y: earthRadiusM * degToRad(p.Lat),
	}
}

func planarDist(a, b planarPoint) float64 {
	return math.Hypot(b.X-a.X, b.Y-a.Y)
}

// snapToLine finds the point on the polyline nearest to p and returns its
// chainage — the distance along the line from the start to the snapped point.
func snapToLine(pl planarLine, p planarPoint) (chainageM float64) {
	best := math.Inf(1)
	for i := 0; i < len(pl.points)-1; i++ {
		a, b := pl.points[i], pl.points[i+1]
		t, closest := closestPointOnSegment(a, b, p)
		d := planarDist(p, closest)
		if d < best {
			best = d
			chainageM = pl.chainageM[i] + t*planarDist(a, b)
		}
	}
	return chainageM
}

// closestPointOnSegment projects p onto the segment a→b, clamped to the
// segment, and returns the clamp parameter t in [0, 1] and the resulting point.
func closestPointOnSegment(a, b, p planarPoint) (t float64, closest planarPoint) {
	dx := b.X - a.X
	dy := b.Y - a.Y
	lenSq := dx*dx + dy*dy
	if lenSq == 0 { // degenerate (duplicate) vertex: the segment is a point
		return 0, a
	}
	t = ((p.X-a.X)*dx + (p.Y-a.Y)*dy) / lenSq
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return t, planarPoint{X: a.X + t*dx, Y: a.Y + t*dy}
}

// lineSegment is one physics-uniform stretch of route line, expressed in
// chainage terms: it runs from StartM to EndM, with Physics applying across
// it. Bundling the two keeps them from drifting out of sync the way parallel
// chainage/physics slices indexed by a shared i would.
type lineSegment struct {
	StartM, EndM float64
	Physics      Segment
}

// buildLineSegments pairs each underlying route segment's physics with the
// chainage span (from planarLn's per-vertex chainage) it covers.
func buildLineSegments(planarLn planarLine, physics []Segment) []lineSegment {
	out := make([]lineSegment, len(physics))
	for i, seg := range physics {
		out[i] = lineSegment{StartM: planarLn.chainageM[i], EndM: planarLn.chainageM[i+1], Physics: seg}
	}
	return out
}

// splitSpan slices lineSegs into the sub-segments falling within
// [fromChainageM, toChainageM), producing one SpanSegment per underlying route
// segment the span fully or partly covers.
func splitSpan(lineSegs []lineSegment, fromChainageM, toChainageM float64) []SpanSegment {
	var out []SpanSegment
	for _, seg := range lineSegs {
		lo := math.Max(seg.StartM, fromChainageM)
		hi := math.Min(seg.EndM, toChainageM)
		if hi-lo <= 0 {
			continue
		}
		out = append(out, SpanSegment{DistanceM: hi - lo, Physics: seg.Physics})
	}
	return out
}
