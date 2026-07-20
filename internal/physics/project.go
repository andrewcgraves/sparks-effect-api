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
	Lng float64
	Lat float64
}

// Segment is the authored track physics for one span between two consecutive
// line coordinates. Its fields mirror transit.RouteSegment; it is defined
// locally so this package stays free of a dependency on internal/transit, the
// same seam internal/route already keeps for its own mirrored Segment type.
type Segment struct {
	CantMM       float64
	CurveRadiusM float64
	GradePct     float64
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
// uncanted track (the zero Segment).
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

	segs := physicsSegs
	if len(segs) == 0 {
		segs = make([]Segment, len(line)-1)
	}

	planarLine, refLatRad := projectLinePlanar(line)
	cum := cumulativeChainage(planarLine)

	type projectedStop struct {
		Stop
		chainageM float64
	}
	projected := make([]projectedStop, len(stops))
	for i, s := range stops {
		x, y := projectPlanar(s.Location, refLatRad)
		projected[i] = projectedStop{Stop: s, chainageM: snapToLine(planarLine, cum, x, y)}
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
			Segments:   splitSpan(cum, segs, from.chainageM, to.chainageM),
		})
	}

	return spans, nil
}

// projectLinePlanar converts a WGS84 line to local planar meters using an
// equirectangular approximation around the line's mean latitude, and returns
// the projected points alongside the reference latitude used (so stops can be
// projected consistently with it).
//
// This trades literal geodesic accuracy for a locally-consistent, self-similar
// distance metric — which is what nearest-point projection and chainage need:
// the same answer whether you sum the pieces or measure the whole, not the
// true great-circle length. It is accurate to a small fraction of a percent at
// the route scales this compiler targets (regional rail lines).
func projectLinePlanar(line []Point) (planar []Point, refLatRad float64) {
	var latSum float64
	for _, p := range line {
		latSum += p.Lat
	}
	refLatRad = (latSum / float64(len(line))) * math.Pi / 180

	planar = make([]Point, len(line))
	for i, p := range line {
		x, y := projectPlanar(p, refLatRad)
		planar[i] = Point{Lng: x, Lat: y} // reused as a plain (x, y) pair in meters
	}
	return planar, refLatRad
}

// projectPlanar converts a single WGS84 point to local planar meters (x, y)
// around refLatRad, using the same equirectangular approximation as
// projectLinePlanar.
func projectPlanar(p Point, refLatRad float64) (x, y float64) {
	latRad := p.Lat * math.Pi / 180
	lngRad := p.Lng * math.Pi / 180
	x = earthRadiusM * lngRad * math.Cos(refLatRad)
	y = earthRadiusM * latRad
	return x, y
}

// cumulativeChainage returns, for each vertex of a planar line, the distance
// along the line from its first point. cum[0] is always 0 and
// len(cum) == len(planarLine).
func cumulativeChainage(planarLine []Point) []float64 {
	cum := make([]float64, len(planarLine))
	for i := 1; i < len(planarLine); i++ {
		cum[i] = cum[i-1] + planarDist(planarLine[i-1], planarLine[i])
	}
	return cum
}

func planarDist(a, b Point) float64 {
	dx := b.Lng - a.Lng
	dy := b.Lat - a.Lat
	return math.Hypot(dx, dy)
}

// snapToLine finds the point on the polyline nearest to (x, y) and returns its
// chainage — the distance along the line from the start to the snapped point.
func snapToLine(planarLine []Point, cum []float64, x, y float64) (chainageM float64) {
	best := math.Inf(1)
	for i := 0; i < len(planarLine)-1; i++ {
		a, b := planarLine[i], planarLine[i+1]
		t, cx, cy := closestPointOnSegment(a, b, x, y)
		d := math.Hypot(x-cx, y-cy)
		if d < best {
			best = d
			chainageM = cum[i] + t*planarDist(a, b)
		}
	}
	return chainageM
}

// closestPointOnSegment projects (x, y) onto the segment a→b, clamped to the
// segment, and returns the clamp parameter t in [0, 1] and the resulting point.
func closestPointOnSegment(a, b Point, x, y float64) (t, cx, cy float64) {
	dx := b.Lng - a.Lng
	dy := b.Lat - a.Lat
	lenSq := dx*dx + dy*dy
	if lenSq == 0 { // degenerate (duplicate) vertex: the segment is a point
		return 0, a.Lng, a.Lat
	}
	t = ((x-a.Lng)*dx + (y-a.Lat)*dy) / lenSq
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return t, a.Lng + t*dx, a.Lat + t*dy
}

// splitSpan slices the underlying per-vertex-segment physics into the
// sub-segments falling within [fromChainageM, toChainageM), producing one
// SpanSegment per underlying route segment the span fully or partly covers.
func splitSpan(cum []float64, segs []Segment, fromChainageM, toChainageM float64) []SpanSegment {
	var out []SpanSegment
	for i, seg := range segs {
		segStart, segEnd := cum[i], cum[i+1]
		lo := math.Max(segStart, fromChainageM)
		hi := math.Min(segEnd, toChainageM)
		if hi-lo <= 0 {
			continue
		}
		out = append(out, SpanSegment{DistanceM: hi - lo, Physics: seg})
	}
	return out
}
