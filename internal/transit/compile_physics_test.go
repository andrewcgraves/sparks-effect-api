package transit

import "testing"

func physicsTestVehicle() VehicleType {
	return VehicleType{
		ID:              "vt-physics",
		MaxSpeedKMH:     36, // 10 m/s, a clean number for hand-worked kinematics
		AccelerationMS2: 1,
		DecelerationMS2: 1,
		FloorHeight:     "high",
		DwellLevelS:     30,
		DwellStepS:      60,
	}
}

// TestCompileServicePhysics_twoStopStraightLine pins the whole pipeline —
// geometry -> stop projection -> speed-profile integration -> dwell -> Edge —
// against an independently hand-worked example.
//
// The route is a straight equatorial line from (0,0) to (1,0) degrees, whose
// great-circle length is the same independently-derived formula
// project_test.go's TestProjectStops_twoStopsAtLineEndpointsOnStraightLine
// already pins: R * deltaRadians = 6371000 * (pi/180) = 111194.926644... m.
//
// With vmax=10 m/s and accel=decel=1 m/s^2 (physicsTestVehicle), the
// accelerate-cruise-decelerate motion time is:
//
//	accel/decel distance: 50 m each (100 m total)
//	cruise distance: 111194.926644 - 100 = 111094.926644 m, at 10 m/s = 11109.4926644 s
//	motion time: 10 + 10 + 11109.4926644 = 11129.4926644 s, rounds to 11129 s
//
// Station b's platform matches the vehicle's floor height, so it dwells
// DwellLevelS (30s); station a's does not, so it dwells DwellStepS (60s) —
// this also exercises that each Edge's Seconds carries its *destination*
// stop's dwell, matching the hand-authored-table compiler's convention in
// compile.go (pathDwellSecs sums dwell over path[1:], excluding the origin).
func TestCompileServicePhysics_twoStopStraightLine(t *testing.T) {
	route := Route{
		ID:   "rt-1",
		Slug: "rt-1",
		Geometry: GeoLineString{
			Type:        "LineString",
			Coordinates: [][]float64{{0, 0}, {1, 0}},
		},
	}
	stations := []Station{
		{ID: "st-a", Slug: "a", Location: GeoPoint{Coordinates: []float64{0, 0}}, PlatformHeight: "low"},
		{ID: "st-b", Slug: "b", Location: GeoPoint{Coordinates: []float64{1, 0}}, PlatformHeight: "high"},
	}
	svc := Service{
		ID:     "svc-1",
		Active: true,
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
		},
	}

	got, err := CompileServicePhysics(route, stations, svc, physicsTestVehicle())
	if err != nil {
		t.Fatalf("CompileServicePhysics() error = %v, want nil", err)
	}
	if got.ServiceID != "svc-1" {
		t.Errorf("ServiceID = %q, want %q", got.ServiceID, "svc-1")
	}
	if len(got.Edges) != 2 {
		t.Fatalf("len(Edges) = %d, want 2", len(got.Edges))
	}

	const motionSecs = 11129
	want := map[[2]string]int{
		{"a", "b"}: motionSecs + 30, // destination b dwells DwellLevelS
		{"b", "a"}: motionSecs + 60, // destination a dwells DwellStepS
	}
	for _, e := range got.Edges {
		key := [2]string{e.FromSlug, e.ToSlug}
		wantSecs, ok := want[key]
		if !ok {
			t.Errorf("unexpected edge %s -> %s", e.FromSlug, e.ToSlug)
			continue
		}
		if e.Seconds != wantSecs {
			t.Errorf("edge %s -> %s: Seconds = %d, want %d", e.FromSlug, e.ToSlug, e.Seconds, wantSecs)
		}
		delete(want, key)
	}
	for k := range want {
		t.Errorf("missing edge %s -> %s", k[0], k[1])
	}
}

// TestCompileServicePhysics_feedsDijkstra proves the Edges CompileServicePhysics
// produces are consumable by the existing TransitGraph/Dijkstra machinery —
// the acceptance criterion that physics-compiled services "feed the
// TransitGraph -> Dijkstra -> isochrone chain."
func TestCompileServicePhysics_feedsDijkstra(t *testing.T) {
	route := Route{
		Geometry: GeoLineString{Coordinates: [][]float64{{0, 0}, {1, 0}}},
	}
	stations := []Station{
		{ID: "st-a", Slug: "a", Location: GeoPoint{Coordinates: []float64{0, 0}}, PlatformHeight: "high"},
		{ID: "st-b", Slug: "b", Location: GeoPoint{Coordinates: []float64{1, 0}}, PlatformHeight: "high"},
	}
	svc := Service{
		ID:     "svc-1",
		Active: true,
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
		},
	}

	sg, err := CompileServicePhysics(route, stations, svc, physicsTestVehicle())
	if err != nil {
		t.Fatalf("CompileServicePhysics() error = %v, want nil", err)
	}

	secs, ok := servicePathSecs(sg, "a", "b")
	if !ok {
		t.Fatal("graphDijkstra: no path a -> b over physics-compiled edges")
	}
	if secs <= 0 {
		t.Errorf("graphDijkstra: secs = %d, want > 0", secs)
	}
}

// TestCompileServicePhysics_threeStopsProduceTwoSpans covers a
// representative multi-stop service: each consecutive stop pair gets its own
// InterStopSpan, so a 3-stop service compiles to 2 forward + 2 reverse edges,
// none of which skip the middle stop.
func TestCompileServicePhysics_threeStopsProduceTwoSpans(t *testing.T) {
	route := Route{
		Geometry: GeoLineString{Coordinates: [][]float64{{0, 0}, {2, 0}}},
	}
	stations := []Station{
		{ID: "st-a", Slug: "a", Location: GeoPoint{Coordinates: []float64{0, 0}}, PlatformHeight: "high"},
		{ID: "st-b", Slug: "b", Location: GeoPoint{Coordinates: []float64{1, 0}}, PlatformHeight: "high"},
		{ID: "st-c", Slug: "c", Location: GeoPoint{Coordinates: []float64{2, 0}}, PlatformHeight: "high"},
	}
	svc := Service{
		ID:     "svc-1",
		Active: true,
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
			{StationID: "st-c", Sequence: 3},
		},
	}

	got, err := CompileServicePhysics(route, stations, svc, physicsTestVehicle())
	if err != nil {
		t.Fatalf("CompileServicePhysics() error = %v, want nil", err)
	}
	if len(got.Edges) != 4 {
		t.Fatalf("len(Edges) = %d, want 4 (a<->b, b<->c)", len(got.Edges))
	}

	wantPairs := map[[2]string]bool{
		{"a", "b"}: true, {"b", "a"}: true,
		{"b", "c"}: true, {"c", "b"}: true,
	}
	for _, e := range got.Edges {
		key := [2]string{e.FromSlug, e.ToSlug}
		if !wantPairs[key] {
			t.Errorf("unexpected edge %s -> %s", e.FromSlug, e.ToSlug)
		}
		delete(wantPairs, key)
	}
	for k := range wantPairs {
		t.Errorf("missing edge %s -> %s", k[0], k[1])
	}
}

// TestCompileServicePhysics_gradeThreadsThroughToRunTime confirms a route's
// per-segment GradePct actually reaches SpanRunSeconds through
// toPhysicsSegments and affects the compiled edge — the physics package's
// own TestSpanRunSeconds_descendingGradeIncreasesTime pins the GradePct/100
// conversion in isolation; this closes the same gap for the wiring between
// RouteSegment and physics.Segment.
func TestCompileServicePhysics_gradeThreadsThroughToRunTime(t *testing.T) {
	stations := []Station{
		{ID: "st-a", Slug: "a", Location: GeoPoint{Coordinates: []float64{0, 0}}, PlatformHeight: "high"},
		{ID: "st-b", Slug: "b", Location: GeoPoint{Coordinates: []float64{1, 0}}, PlatformHeight: "high"},
	}
	svc := Service{
		ID:     "svc-1",
		Active: true,
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
		},
	}
	vt := physicsTestVehicle()

	levelRoute := Route{Geometry: GeoLineString{Coordinates: [][]float64{{0, 0}, {1, 0}}}}
	gradedRoute := Route{
		Geometry: GeoLineString{Coordinates: [][]float64{{0, 0}, {1, 0}}},
		Segments: []RouteSegment{{GradePct: -10}},
	}

	level, err := CompileServicePhysics(levelRoute, stations, svc, vt)
	if err != nil {
		t.Fatalf("CompileServicePhysics(level) error = %v, want nil", err)
	}
	graded, err := CompileServicePhysics(gradedRoute, stations, svc, vt)
	if err != nil {
		t.Fatalf("CompileServicePhysics(graded) error = %v, want nil", err)
	}

	levelSecs, ok := servicePathSecs(level, "a", "b")
	if !ok {
		t.Fatal("no path a -> b over level edges")
	}
	gradedSecs, ok := servicePathSecs(graded, "a", "b")
	if !ok {
		t.Fatal("no path a -> b over graded edges")
	}
	if gradedSecs <= levelSecs {
		t.Errorf("descending-grade edge time %d should exceed level edge time %d", gradedSecs, levelSecs)
	}
}

func TestCompileServicePhysics_errorsOnUnknownStation(t *testing.T) {
	route := Route{Geometry: GeoLineString{Coordinates: [][]float64{{0, 0}, {1, 0}}}}
	stations := []Station{
		{ID: "st-a", Slug: "a", Location: GeoPoint{Coordinates: []float64{0, 0}}},
	}
	svc := Service{
		ID:     "svc-1",
		Active: true,
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-missing", Sequence: 2},
		},
	}

	if _, err := CompileServicePhysics(route, stations, svc, physicsTestVehicle()); err == nil {
		t.Error("CompileServicePhysics() error = nil, want an error for an unknown station id")
	}
}

func TestCompileServicePhysics_errorsOnRouteWithFewerThanTwoPoints(t *testing.T) {
	route := Route{Geometry: GeoLineString{Coordinates: [][]float64{{0, 0}}}}
	stations := []Station{
		{ID: "st-a", Slug: "a", Location: GeoPoint{Coordinates: []float64{0, 0}}},
		{ID: "st-b", Slug: "b", Location: GeoPoint{Coordinates: []float64{1, 0}}},
	}
	svc := Service{
		ID:     "svc-1",
		Active: true,
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
		},
	}

	if _, err := CompileServicePhysics(route, stations, svc, physicsTestVehicle()); err == nil {
		t.Error("CompileServicePhysics() error = nil, want an error for a route with < 2 geometry points")
	}
}

// TestCompileServicePhysics_inactiveServiceReturnsEmptyGraph matches
// Compile's convention (compile.go: `if !svc.Active { continue }`) of
// contributing nothing to the TransitGraph for an inactive service.
func TestCompileServicePhysics_inactiveServiceReturnsEmptyGraph(t *testing.T) {
	route := Route{Geometry: GeoLineString{Coordinates: [][]float64{{0, 0}, {1, 0}}}}
	stations := []Station{
		{ID: "st-a", Slug: "a", Location: GeoPoint{Coordinates: []float64{0, 0}}},
		{ID: "st-b", Slug: "b", Location: GeoPoint{Coordinates: []float64{1, 0}}},
	}
	svc := Service{
		ID:     "svc-1",
		Active: false,
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
		},
	}

	got, err := CompileServicePhysics(route, stations, svc, physicsTestVehicle())
	if err != nil {
		t.Fatalf("CompileServicePhysics() error = %v, want nil", err)
	}
	if len(got.Edges) != 0 {
		t.Errorf("len(Edges) = %d, want 0 for an inactive service", len(got.Edges))
	}
}

func TestCompileServicePhysics_errorsOnSegmentCountMismatch(t *testing.T) {
	route := Route{
		Geometry: GeoLineString{Coordinates: [][]float64{{0, 0}, {1, 0}, {2, 0}}},
		Segments: []RouteSegment{{}}, // 2 points' worth, but the line has 3
	}
	stations := []Station{
		{ID: "st-a", Slug: "a", Location: GeoPoint{Coordinates: []float64{0, 0}}},
		{ID: "st-b", Slug: "b", Location: GeoPoint{Coordinates: []float64{2, 0}}},
	}
	svc := Service{
		ID:     "svc-1",
		Active: true,
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
		},
	}

	if _, err := CompileServicePhysics(route, stations, svc, physicsTestVehicle()); err == nil {
		t.Error("CompileServicePhysics() error = nil, want an error on a route/segment count mismatch")
	}
}
