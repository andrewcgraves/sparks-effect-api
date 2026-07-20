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
	// An L-shaped line: north from (0,0) to (0,1), then east to (1,1).
	line := []Point{
		{Lng: 0.0, Lat: 0.0},
		{Lng: 0.0, Lat: 1.0},
		{Lng: 1.0, Lat: 1.0},
	}
	stops := []Stop{
		{ID: "start", Location: Point{Lng: 0.0, Lat: 0.0}},
		{ID: "corner", Location: Point{Lng: 0.0, Lat: 1.0}},
		{ID: "end", Location: Point{Lng: 1.0, Lat: 1.0}},
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

	// The projection uses a single equirectangular reference latitude for the
	// whole line (its mean latitude, documented on projectLinePlanar) rather
	// than a per-segment one, so the east-west leg at lat=1 is foreshortened by
	// cos(meanLat) relative to the north-south leg. Both legs below are
	// computed independently from that documented formula, not copied from the
	// implementation's control flow.
	const earthR = 6371000.0
	meanLatRad := (0.0 + 1.0 + 1.0) / 3.0 * math.Pi / 180.0
	nsLegM := earthR * (1.0 * math.Pi / 180.0)
	ewLegM := earthR * (1.0 * math.Pi / 180.0) * math.Cos(meanLatRad)

	if math.Abs(got[0].DistanceM-nsLegM) > distTol {
		t.Errorf("got[0].DistanceM = %v, want ~%v", got[0].DistanceM, nsLegM)
	}
	if math.Abs(got[1].DistanceM-ewLegM) > distTol {
		t.Errorf("got[1].DistanceM = %v, want ~%v", got[1].DistanceM, ewLegM)
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
