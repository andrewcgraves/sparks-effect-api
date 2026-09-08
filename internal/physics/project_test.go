package physics

import (
	"math"
	"strings"
	"testing"
)

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

func TestProjectStops_threeStopsProduceTwoOrderedSpans(t *testing.T) {

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

func haversineM(a, b Point) float64 {
	const r = 6371000.0
	lat1, lat2 := a.Lat*math.Pi/180, b.Lat*math.Pi/180
	dLat := (b.Lat - a.Lat) * math.Pi / 180
	dLng := (b.Lng - a.Lng) * math.Pi / 180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return r * 2 * math.Atan2(math.Sqrt(h), math.Sqrt(1-h))
}

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

func TestSnapStops_returnsChainagePreservingInputOrder(t *testing.T) {
	line := []Point{{Lng: 0, Lat: 0}, {Lng: 0, Lat: 1}}
	// Supplied in reverse chainage order on purpose.
	stops := []Stop{
		{ID: "far", Location: Point{Lng: 0, Lat: 1}},
		{ID: "near", Location: Point{Lng: 0, Lat: 0}},
	}

	got, err := SnapStops(line, stops)
	if err != nil {
		t.Fatalf("SnapStops() error = %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}

	if got[0].ID != "far" || got[1].ID != "near" {
		t.Fatalf("IDs = (%s, %s), want (far, near) — input order must be preserved",
			got[0].ID, got[1].ID)
	}

	wantFarM := haversineM(line[0], line[1]) // haversine ground truth
	if math.Abs(got[0].ChainageM-wantFarM) > distTol {
		t.Errorf("got[0].ChainageM = %v, want ~%v", got[0].ChainageM, wantFarM)
	}
	if math.Abs(got[1].ChainageM) > distTol {
		t.Errorf("got[1].ChainageM = %v, want ~0", got[1].ChainageM)
	}
}

func TestSnapStops_offLineStopReturnsThePointOnTheLine(t *testing.T) {
	const lineLng = -119.78
	line := []Point{{Lng: lineLng, Lat: 36.75}, {Lng: lineLng, Lat: 36.85}}
	stops := []Stop{{ID: "east-of-line", Location: Point{Lng: lineLng + 0.01, Lat: 36.80}}}

	got, err := SnapStops(line, stops)
	if err != nil {
		t.Fatalf("SnapStops() error = %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}

	const degTol = 1e-6
	if math.Abs(got[0].Point.Lng-lineLng) > degTol {
		t.Errorf("got[0].Point.Lng = %v, want ~%v (the snapped point lies on the line)", got[0].Point.Lng, lineLng)
	}
	if math.Abs(got[0].Point.Lat-36.80) > degTol {
		t.Errorf("got[0].Point.Lat = %v, want ~36.80 (foot of the perpendicular)", got[0].Point.Lat)
	}
}

func TestSnapStops_offsetIsDistanceFromRawInputToSnappedPoint(t *testing.T) {
	const lineLng = -119.78
	line := []Point{{Lng: lineLng, Lat: 36.75}, {Lng: lineLng, Lat: 36.85}}
	offLine := Point{Lng: lineLng + 0.01, Lat: 36.80}
	stops := []Stop{
		{ID: "on-line", Location: Point{Lng: lineLng, Lat: 36.80}},
		{ID: "east-of-line", Location: offLine},
	}

	got, err := SnapStops(line, stops)
	if err != nil {
		t.Fatalf("SnapStops() error = %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}

	if math.Abs(got[0].OffsetM) > distTol {
		t.Errorf("got[0].OffsetM = %v, want ~0 for a stop already on the line", got[0].OffsetM)
	}

	// The perpendicular from the stop meets the meridian at its own latitude,
	// so the offset is the east-west great-circle distance between the two —
	// haversine ground truth, independent of the planar frame under test.
	wantOffsetM := haversineM(offLine, Point{Lng: lineLng, Lat: 36.80})
	if math.Abs(got[1].OffsetM-wantOffsetM) > distTol {
		t.Errorf("got[1].OffsetM = %v, want ~%v (±%v)", got[1].OffsetM, wantOffsetM, distTol)
	}
}

func TestSnapStops_errorsOnShortLine(t *testing.T) {
	stops := []Stop{{ID: "a", Location: Point{Lng: 0, Lat: 0}}}

	for _, tc := range []struct {
		name string
		line []Point
	}{
		{name: "single point", line: []Point{{Lng: 0, Lat: 0}}},
		{name: "empty line", line: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SnapStops(tc.line, stops)
			if err == nil {
				t.Fatalf("SnapStops() error = nil, want error containing %q", "at least 2")
			}
			if !strings.Contains(err.Error(), "at least 2") {
				t.Errorf("SnapStops() error = %q, want it to contain %q", err.Error(), "at least 2")
			}
		})
	}
}

func TestSnapStops_acceptsFewerThanTwoStops(t *testing.T) {
	line := []Point{{Lng: 0, Lat: 0}, {Lng: 0, Lat: 1}}

	got, err := SnapStops(line, []Stop{{ID: "only", Location: Point{Lng: 0, Lat: 0.5}}})
	if err != nil {
		t.Fatalf("SnapStops() with one stop error = %v, want nil", err)
	}
	if len(got) != 1 || got[0].ID != "only" {
		t.Fatalf("got = %+v, want exactly one snapped stop with ID %q", got, "only")
	}
	wantChainageM := haversineM(line[0], Point{Lng: 0, Lat: 0.5})
	if math.Abs(got[0].ChainageM-wantChainageM) > distTol {
		t.Errorf("got[0].ChainageM = %v, want ~%v", got[0].ChainageM, wantChainageM)
	}

	empty, err := SnapStops(line, nil)
	if err != nil {
		t.Fatalf("SnapStops() with no stops error = %v, want nil", err)
	}
	if len(empty) != 0 {
		t.Errorf("SnapStops() with no stops = %+v, want empty", empty)
	}
}

func TestDistanceM_matchesHaversineGroundTruth(t *testing.T) {
	cases := []struct {
		name string
		a, b Point
	}{
		// Around San Francisco, roughly the separations a merge decides on.
		{"east-west 80 m", Point{Lng: -122.397, Lat: 37.790}, Point{Lng: -122.3961, Lat: 37.790}},
		{"north-south 110 m", Point{Lng: -122.397, Lat: 37.790}, Point{Lng: -122.397, Lat: 37.791}},
		{"diagonal", Point{Lng: -122.397, Lat: 37.790}, Point{Lng: -122.3958, Lat: 37.7907}},
		{"coincident", Point{Lng: -122.397, Lat: 37.790}, Point{Lng: -122.397, Lat: 37.790}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := haversineM(tc.a, tc.b)
			got := DistanceM(tc.a, tc.b)
			if math.Abs(got-want) > 0.01 {
				t.Errorf("DistanceM(%v, %v) = %v, want ~%v (haversine)", tc.a, tc.b, got, want)
			}
		})
	}
}

func TestDistanceM_isSymmetric(t *testing.T) {
	a := Point{Lng: -122.397, Lat: 37.790}
	b := Point{Lng: -122.3958, Lat: 37.7907}
	if ab, ba := DistanceM(a, b), DistanceM(b, a); ab != ba {
		t.Errorf("DistanceM(a,b) = %v, DistanceM(b,a) = %v, want identical", ab, ba)
	}
}
