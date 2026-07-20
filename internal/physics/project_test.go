package physics

import (
	"math"
	"strings"
	"testing"
)

// distTol is the absolute tolerance, in meters, used when comparing computed
// distances against a hand-checked reference value.
const distTol = 1.0

func TestProjectStops_twoStopsAtLineEndpointsOnStraightLine(t *testing.T) {
	// A straight east-west line at the equator. The great-circle distance along
	// the equator for a longitude delta is exactly R * deltaRadians (mean Earth
	// radius R = 6371 km, matching the haversine convention already used in
	// internal/isochrone) — an independently-computed reference value.
	line := []Point{
		{Lng: 0.0, Lat: 0.0},
		{Lng: 1.0, Lat: 0.0},
	}
	stops := []Stop{
		{ID: "a", Location: Point{Lng: 0.0, Lat: 0.0}},
		{ID: "b", Location: Point{Lng: 1.0, Lat: 0.0}},
	}

	got, err := ProjectStops(line, nil, stops)
	if err != nil {
		t.Fatalf("ProjectStops() error = %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}

	span := got[0]
	if span.FromStopID != "a" || span.ToStopID != "b" {
		t.Errorf("span endpoints = (%s, %s), want (a, b)", span.FromStopID, span.ToStopID)
	}

	wantDistM := 6371000.0 * (1.0 * math.Pi / 180.0)
	if math.Abs(span.DistanceM-wantDistM) > distTol {
		t.Errorf("span.DistanceM = %v, want ~%v (±%v)", span.DistanceM, wantDistM, distTol)
	}
}

// TestProjectStops_offLineStopSnapsToNearestPointIgnoringPerpendicularOffset
// covers the "snap to nearest point on the line" acceptance criterion
// directly: a stop that is not exactly on the line must still land at the
// correct chainage — its perpendicular distance from the line must not affect
// the along-line (chainage) component of the projection.
func TestProjectStops_offLineStopSnapsToNearestPointIgnoringPerpendicularOffset(t *testing.T) {
	// A north-south line (constant longitude), so the expected chainage to the
	// midpoint is an independently-computed reference value: half the line's
	// total north-south distance, regardless of how far east the stop sits.
	line := []Point{
		{Lng: 0.0, Lat: 0.0},
		{Lng: 0.0, Lat: 1.0},
	}
	stops := []Stop{
		{ID: "a", Location: Point{Lng: 0.0, Lat: 0.0}},
		// Offset well to the east of the line's midpoint latitude.
		{ID: "mid", Location: Point{Lng: 0.01, Lat: 0.5}},
	}

	got, err := ProjectStops(line, nil, stops)
	if err != nil {
		t.Fatalf("ProjectStops() error = %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}

	wantChainageM := 6371000.0 * (0.5 * math.Pi / 180.0)
	span := got[0]
	if math.Abs(span.DistanceM-wantChainageM) > distTol {
		t.Errorf("span.DistanceM = %v, want ~%v (±%v) — perpendicular offset must not affect chainage",
			span.DistanceM, wantChainageM, distTol)
	}
}

// TestProjectStops_reordersStopsByChainageAndSplitsAcrossVertex covers two
// acceptance criteria at once: stops supplied out of route order come back
// ordered by chainage, and a span whose endpoints straddle an interior route
// vertex is split into one SpanSegment per underlying route segment it
// crosses, each carrying that segment's own physics.
func TestProjectStops_reordersStopsByChainageAndSplitsAcrossVertex(t *testing.T) {
	// A north-south line with one interior vertex at lat=1, so it has two
	// physics-distinct segments of equal, independently-computed length.
	line := []Point{
		{Lng: 0.0, Lat: 0.0},
		{Lng: 0.0, Lat: 1.0},
		{Lng: 0.0, Lat: 2.0},
	}
	segs := []Segment{
		{CantMM: 100, CurveRadiusM: 1000, GradePct: 1.0},
		{CantMM: 200, CurveRadiusM: 2000, GradePct: -1.0},
	}
	// Supplied in reverse chainage order on purpose.
	stops := []Stop{
		{ID: "far", Location: Point{Lng: 0.0, Lat: 2.0}},
		{ID: "near", Location: Point{Lng: 0.0, Lat: 0.0}},
	}

	got, err := ProjectStops(line, segs, stops)
	if err != nil {
		t.Fatalf("ProjectStops() error = %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}

	span := got[0]
	if span.FromStopID != "near" || span.ToStopID != "far" {
		t.Fatalf("span endpoints = (%s, %s), want (near, far) — stops must be reordered by chainage",
			span.FromStopID, span.ToStopID)
	}

	if len(span.Segments) != 2 {
		t.Fatalf("len(span.Segments) = %d, want 2 (one per underlying route segment crossed)", len(span.Segments))
	}

	legM := 6371000.0 * (1.0 * math.Pi / 180.0) // lat 0->1 and lat 1->2 are equal legs
	if math.Abs(span.Segments[0].DistanceM-legM) > distTol {
		t.Errorf("span.Segments[0].DistanceM = %v, want ~%v", span.Segments[0].DistanceM, legM)
	}
	if span.Segments[0].Physics != segs[0] {
		t.Errorf("span.Segments[0].Physics = %+v, want %+v", span.Segments[0].Physics, segs[0])
	}
	if math.Abs(span.Segments[1].DistanceM-legM) > distTol {
		t.Errorf("span.Segments[1].DistanceM = %v, want ~%v", span.Segments[1].DistanceM, legM)
	}
	if span.Segments[1].Physics != segs[1] {
		t.Errorf("span.Segments[1].Physics = %+v, want %+v", span.Segments[1].Physics, segs[1])
	}

	wantTotal := 2 * legM
	if math.Abs(span.DistanceM-wantTotal) > distTol {
		t.Errorf("span.DistanceM = %v, want ~%v", span.DistanceM, wantTotal)
	}
}

// TestProjectStops_threeStopsProduceTwoOrderedSpans covers a representative
// multi-stop service pattern: three stops along a bent (non-straight) line
// must produce exactly two spans, in chainage order, each summing to the
// correct leg distance.
func TestProjectStops_threeStopsProduceTwoOrderedSpans(t *testing.T) {
	// An L-shaped line near Fresno, CA (representative of the CA-HSR corridor
	// this compiler targets): north for one leg, then east for the other. Legs
	// are ~1.1 km, small enough that the implementation's equirectangular
	// approximation and true great-circle (haversine) distance agree to well
	// under a meter, so haversineM below — a fresh, independent implementation
	// of the standard formula — is a real ground truth, not a restatement of
	// the code under test.
	const (
		baseLng  = -119.78
		baseLat  = 36.75
		deltaDeg = 0.01
	)
	line := []Point{
		{Lng: baseLng, Lat: baseLat},
		{Lng: baseLng, Lat: baseLat + deltaDeg},
		{Lng: baseLng + deltaDeg, Lat: baseLat + deltaDeg},
	}
	stops := []Stop{
		{ID: "start", Location: line[0]},
		{ID: "corner", Location: line[1]},
		{ID: "end", Location: line[2]},
	}

	got, err := ProjectStops(line, nil, stops)
	if err != nil {
		t.Fatalf("ProjectStops() error = %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}

	if got[0].FromStopID != "start" || got[0].ToStopID != "corner" {
		t.Errorf("got[0] endpoints = (%s, %s), want (start, corner)", got[0].FromStopID, got[0].ToStopID)
	}
	if got[1].FromStopID != "corner" || got[1].ToStopID != "end" {
		t.Errorf("got[1] endpoints = (%s, %s), want (corner, end)", got[1].FromStopID, got[1].ToStopID)
	}

	nsLegM := haversineM(line[0], line[1])
	ewLegM := haversineM(line[1], line[2])

	if math.Abs(got[0].DistanceM-nsLegM) > distTol {
		t.Errorf("got[0].DistanceM = %v, want ~%v (haversine ground truth)", got[0].DistanceM, nsLegM)
	}
	if math.Abs(got[1].DistanceM-ewLegM) > distTol {
		t.Errorf("got[1].DistanceM = %v, want ~%v (haversine ground truth)", got[1].DistanceM, ewLegM)
	}
}

// haversineM is an independent great-circle distance implementation (the
// standard haversine formula, mean Earth radius 6371 km) used purely as test
// ground truth. It is not shared with — and must not be kept in sync with —
// the equirectangular approximation project.go uses internally.
func haversineM(a, b Point) float64 {
	const r = 6371000.0
	lat1, lat2 := a.Lat*math.Pi/180, b.Lat*math.Pi/180
	dLat := (b.Lat - a.Lat) * math.Pi / 180
	dLng := (b.Lng - a.Lng) * math.Pi / 180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return r * 2 * math.Atan2(math.Sqrt(h), math.Sqrt(1-h))
}

// TestProjectStops_stopsBeyondLineEndsClampToEndpoints covers the clamped
// nearest-point behavior for a stop that falls before the line's start or
// after its end (e.g. a station sited slightly past the mapped end of a
// route): it must snap to that endpoint rather than extrapolating off the
// line, so both stops land at the line's own endpoint chainages.
func TestProjectStops_stopsBeyondLineEndsClampToEndpoints(t *testing.T) {
	line := []Point{
		{Lng: 0.0, Lat: 0.0},
		{Lng: 0.0, Lat: 1.0},
	}
	stops := []Stop{
		{ID: "before-start", Location: Point{Lng: 0.0, Lat: -0.5}}, // past the start
		{ID: "after-end", Location: Point{Lng: 0.0, Lat: 1.5}},     // past the end
	}

	got, err := ProjectStops(line, nil, stops)
	if err != nil {
		t.Fatalf("ProjectStops() error = %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}

	span := got[0]
	if span.FromStopID != "before-start" || span.ToStopID != "after-end" {
		t.Fatalf("span endpoints = (%s, %s), want (before-start, after-end)", span.FromStopID, span.ToStopID)
	}

	wantDistM := haversineM(line[0], line[1])
	if math.Abs(span.DistanceM-wantDistM) > distTol {
		t.Errorf("span.DistanceM = %v, want ~%v (both stops clamped to the line's own endpoints)",
			span.DistanceM, wantDistM)
	}
}

// TestProjectStops_spanCrossingTwoInteriorVerticesSplitsIntoThreeSegments
// covers a span that is not adjacent to a single route vertex pair: it
// crosses two interior vertices, so it must be split into one SpanSegment per
// underlying route segment (three), each carrying that segment's own physics
// and summing back to the span's total distance.
func TestProjectStops_spanCrossingTwoInteriorVerticesSplitsIntoThreeSegments(t *testing.T) {
	line := []Point{
		{Lng: 0.0, Lat: 0.0},
		{Lng: 0.0, Lat: 1.0},
		{Lng: 0.0, Lat: 2.0},
		{Lng: 0.0, Lat: 3.0},
	}
	segs := []Segment{
		{CantMM: 50, CurveRadiusM: 500, GradePct: 0.5},
		{CantMM: 100, CurveRadiusM: 1000, GradePct: 1.0},
		{CantMM: 150, CurveRadiusM: 1500, GradePct: 1.5},
	}
	stops := []Stop{
		{ID: "a", Location: line[0]},
		{ID: "b", Location: line[3]},
	}

	got, err := ProjectStops(line, segs, stops)
	if err != nil {
		t.Fatalf("ProjectStops() error = %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}

	span := got[0]
	if len(span.Segments) != 3 {
		t.Fatalf("len(span.Segments) = %d, want 3 (one per underlying route segment crossed)", len(span.Segments))
	}
	for i, seg := range segs {
		if span.Segments[i].Physics != seg {
			t.Errorf("span.Segments[%d].Physics = %+v, want %+v", i, span.Segments[i].Physics, seg)
		}
	}

	var summed float64
	for _, ss := range span.Segments {
		summed += ss.DistanceM
	}
	if math.Abs(summed-span.DistanceM) > distTol {
		t.Errorf("sum of SpanSegment distances = %v, want equal to span.DistanceM = %v", summed, span.DistanceM)
	}
}

func TestProjectStops_errors(t *testing.T) {
	validLine := []Point{{Lng: 0, Lat: 0}, {Lng: 0, Lat: 1}, {Lng: 0, Lat: 2}}
	validStops := []Stop{
		{ID: "a", Location: Point{Lng: 0, Lat: 0}},
		{ID: "b", Location: Point{Lng: 0, Lat: 2}},
	}

	tests := []struct {
		name    string
		line    []Point
		segs    []Segment
		stops   []Stop
		wantErr string
	}{
		{
			name:    "line too short",
			line:    []Point{{Lng: 0, Lat: 0}},
			segs:    nil,
			stops:   validStops,
			wantErr: "at least 2",
		},
		{
			name:    "empty line",
			line:    nil,
			segs:    nil,
			stops:   validStops,
			wantErr: "at least 2",
		},
		{
			name:    "mismatched physics length",
			line:    validLine,
			segs:    []Segment{{CantMM: 100}}, // validLine needs 2 segments, not 1
			stops:   validStops,
			wantErr: "2",
		},
		{
			name:    "fewer than two stops",
			line:    validLine,
			segs:    nil,
			stops:   []Stop{{ID: "a", Location: Point{Lng: 0, Lat: 0}}},
			wantErr: "at least 2",
		},
		{
			name:    "no stops",
			line:    validLine,
			segs:    nil,
			stops:   nil,
			wantErr: "at least 2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ProjectStops(tc.line, tc.segs, tc.stops)
			if err == nil {
				t.Fatalf("ProjectStops() error = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("ProjectStops() error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestProjectStops_emptyPhysicsMeansTangentLevelTrack covers the documented
// default: an omitted physics slice must not error, and every resulting
// SpanSegment must carry the zero Segment value.
func TestProjectStops_emptyPhysicsMeansTangentLevelTrack(t *testing.T) {
	line := []Point{{Lng: 0, Lat: 0}, {Lng: 0, Lat: 1}}
	stops := []Stop{
		{ID: "a", Location: Point{Lng: 0, Lat: 0}},
		{ID: "b", Location: Point{Lng: 0, Lat: 1}},
	}

	got, err := ProjectStops(line, nil, stops)
	if err != nil {
		t.Fatalf("ProjectStops() error = %v, want nil", err)
	}
	if len(got) != 1 || len(got[0].Segments) != 1 {
		t.Fatalf("got = %+v, want exactly 1 span with 1 SpanSegment", got)
	}
	if got[0].Segments[0].Physics != (Segment{}) {
		t.Errorf("Segments[0].Physics = %+v, want zero value", got[0].Segments[0].Physics)
	}
}

// TestProjectStops_coincidentStopsProduceZeroDistanceSpan covers the
// degenerate case of two stops snapping to (almost) the same chainage: the
// function must not panic or error, and must return a valid, empty span
// rather than a negative distance.
func TestProjectStops_coincidentStopsProduceZeroDistanceSpan(t *testing.T) {
	line := []Point{{Lng: 0, Lat: 0}, {Lng: 0, Lat: 1}}
	stops := []Stop{
		{ID: "a", Location: Point{Lng: 0, Lat: 0.5}},
		{ID: "b", Location: Point{Lng: 0, Lat: 0.5}},
	}

	got, err := ProjectStops(line, nil, stops)
	if err != nil {
		t.Fatalf("ProjectStops() error = %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].DistanceM < 0 {
		t.Errorf("span.DistanceM = %v, want >= 0", got[0].DistanceM)
	}
	if math.Abs(got[0].DistanceM) > distTol {
		t.Errorf("span.DistanceM = %v, want ~0 for coincident stops", got[0].DistanceM)
	}
}
