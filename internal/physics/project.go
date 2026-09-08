package physics

import (
	"fmt"
	"math"
	"sort"
)

const earthRadiusM = 6371000.0

type Point struct {
	Lng float64
	Lat float64
}

type Segment struct {
	CantMM       float64
	CurveRadiusM float64
	GradePct     float64
}

type Stop struct {
	ID       string
	Location Point
}

type SpanSegment struct {
	DistanceM float64
	Physics   Segment
}

type InterStopSpan struct {
	FromStopID    string
	ToStopID      string
	DistanceM     float64
	FromChainageM float64
	ToChainageM   float64
	Segments      []SpanSegment
}

type SnappedStop struct {
	ID        string
	Point     Point
	ChainageM float64
	OffsetM   float64
}

func SnapStops(line []Point, stops []Stop) ([]SnappedStop, error) {
	if len(line) < 2 {
		return nil, fmt.Errorf("line must have at least 2 points, got %d", len(line))
	}
	return projectLinePlanar(line).snap(stops), nil
}

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

	projected := pl.snap(stops)
	sort.SliceStable(projected, func(i, j int) bool {
		return projected[i].ChainageM < projected[j].ChainageM
	})

	spans := make([]InterStopSpan, 0, len(projected)-1)
	for i := 0; i < len(projected)-1; i++ {
		from, to := projected[i], projected[i+1]
		spans = append(spans, InterStopSpan{
			FromStopID:    from.ID,
			ToStopID:      to.ID,
			DistanceM:     to.ChainageM - from.ChainageM,
			FromChainageM: from.ChainageM,
			ToChainageM:   to.ChainageM,
			Segments:      splitSpan(lineSegs, from.ChainageM, to.ChainageM),
		})
	}

	return spans, nil
}

func DistanceM(a, b Point) float64 {
	refLatRad := degToRad((a.Lat + b.Lat) / 2)
	return planarDist(projectPoint(a, refLatRad), projectPoint(b, refLatRad))
}

type planarPoint struct {
	X, Y float64
}

type planarLine struct {
	points    []planarPoint
	chainageM []float64
	refLatRad float64
}

func degToRad(deg float64) float64 {
	return deg * math.Pi / 180
}

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

func projectPoint(p Point, refLatRad float64) planarPoint {
	return planarPoint{
		X: earthRadiusM * degToRad(p.Lng) * math.Cos(refLatRad),
		Y: earthRadiusM * degToRad(p.Lat),
	}
}

func unprojectPoint(p planarPoint, refLatRad float64) Point {
	return Point{
		Lng: radToDeg(p.X / (earthRadiusM * math.Cos(refLatRad))),
		Lat: radToDeg(p.Y / earthRadiusM),
	}
}

func radToDeg(rad float64) float64 {
	return rad * 180 / math.Pi
}

func planarDist(a, b planarPoint) float64 {
	return math.Hypot(b.X-a.X, b.Y-a.Y)
}

func (pl planarLine) snap(stops []Stop) []SnappedStop {
	out := make([]SnappedStop, len(stops))
	for i, s := range stops {
		p := projectPoint(s.Location, pl.refLatRad)
		chainageM, snapped := pl.snapPoint(p)
		out[i] = SnappedStop{
			ID:        s.ID,
			Point:     unprojectPoint(snapped, pl.refLatRad),
			ChainageM: chainageM,
			OffsetM:   planarDist(p, snapped),
		}
	}
	return out
}

func (pl planarLine) snapPoint(p planarPoint) (chainageM float64, snapped planarPoint) {
	best := math.Inf(1)
	for i := 0; i < len(pl.points)-1; i++ {
		a, b := pl.points[i], pl.points[i+1]
		t, closest := closestPointOnSegment(a, b, p)
		d := planarDist(p, closest)
		if d < best {
			best = d
			chainageM = pl.chainageM[i] + t*planarDist(a, b)
			snapped = closest
		}
	}
	return chainageM, snapped
}

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

type lineSegment struct {
	StartM, EndM float64
	Physics      Segment
}

func buildLineSegments(planarLn planarLine, physics []Segment) []lineSegment {
	out := make([]lineSegment, len(physics))
	for i, seg := range physics {
		out[i] = lineSegment{StartM: planarLn.chainageM[i], EndM: planarLn.chainageM[i+1], Physics: seg}
	}
	return out
}

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
