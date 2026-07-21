package transit

import "testing"

// adapterStations mirrors the seeded fixtures used across the compile tests:
// station a's platform does not match the vehicle's floor height, station b's
// does, so the two exercise both arms of resolveDwell.
func adapterStations() []Station {
	return []Station{
		{ID: "st-a", Slug: "a", Location: GeoPoint{Coordinates: []float64{0, 0}}, PlatformHeight: "low"},
		{ID: "st-b", Slug: "b", Location: GeoPoint{Coordinates: []float64{1, 0}}, PlatformHeight: "high"},
	}
}

func adapterRoute() Route {
	return Route{
		ID:       "rt-1",
		Slug:     "rt-1",
		Geometry: GeoLineString{Type: "LineString", Coordinates: [][]float64{{0, 0}, {1, 0}}},
	}
}

// The seeded adapter's whole job: turn station references into positions and
// turn the platform-height/floor-height comparison into an already-decided
// dwell, so the compiler never sees Station or VehicleType.
func TestCompilableFromService_projectsStationsAndResolvesDwell(t *testing.T) {
	svc := Service{
		ID: "svc-1",
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
		},
		FrequencyWindows: []FrequencyWindow{{StartTime: "06:00", EndTime: "09:00", HeadwayS: 600}},
	}

	got, err := CompilableFromService(adapterRoute(), adapterStations(), svc, physicsTestVehicle())
	if err != nil {
		t.Fatalf("CompilableFromService() error = %v, want nil", err)
	}

	if got.ID != "svc-1" {
		t.Errorf("ID = %q, want svc-1", got.ID)
	}
	if got.Route.ID != "rt-1" {
		t.Errorf("Route.ID = %q, want rt-1", got.Route.ID)
	}
	if got.Vehicle.MaxSpeedKMH != 36 {
		t.Errorf("Vehicle.MaxSpeedKMH = %v, want 36", got.Vehicle.MaxSpeedKMH)
	}
	if len(got.Windows) != 1 || got.Windows[0].HeadwayS != 600 {
		t.Errorf("Windows = %+v, want the service's single 600s window", got.Windows)
	}

	want := []CompiledStop{
		// a: platform "low" != floor "high", so DwellStepS (60).
		{Slug: "a", Lat: 0, Lng: 0, DwellS: 60},
		// b: platform "high" == floor "high", so DwellLevelS (30).
		{Slug: "b", Lat: 0, Lng: 1, DwellS: 30},
	}
	if len(got.Stops) != len(want) {
		t.Fatalf("len(Stops) = %d, want %d", len(got.Stops), len(want))
	}
	for i, w := range want {
		if got.Stops[i] != w {
			t.Errorf("Stops[%d] = %+v, want %+v", i, got.Stops[i], w)
		}
	}
}

// Slice order must not be trusted — Sequence is what orders a seeded stopping
// pattern, and the compiler consumes Stops in order.
func TestCompilableFromService_ordersStopsBySequence(t *testing.T) {
	svc := Service{
		ID: "svc-1",
		Stops: []ServiceStop{
			{StationID: "st-b", Sequence: 2},
			{StationID: "st-a", Sequence: 1},
		},
	}

	got, err := CompilableFromService(adapterRoute(), adapterStations(), svc, physicsTestVehicle())
	if err != nil {
		t.Fatalf("CompilableFromService() error = %v, want nil", err)
	}
	if got.Stops[0].Slug != "a" || got.Stops[1].Slug != "b" {
		t.Errorf("stop slugs = %q, %q, want a, b in Sequence order", got.Stops[0].Slug, got.Stops[1].Slug)
	}
}

// A per-stop dwell override wins over the platform/floor comparison, matching
// resolveDwell's existing precedence.
func TestCompilableFromService_perStopDwellOverrideWins(t *testing.T) {
	override := 5
	svc := Service{
		ID: "svc-1",
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2, DwellS: &override},
		},
	}

	got, err := CompilableFromService(adapterRoute(), adapterStations(), svc, physicsTestVehicle())
	if err != nil {
		t.Fatalf("CompilableFromService() error = %v, want nil", err)
	}
	if got.Stops[1].DwellS != 5 {
		t.Errorf("Stops[1].DwellS = %d, want the 5s override", got.Stops[1].DwellS)
	}
}

// Resolving a station reference is the adapter's responsibility, so an unknown
// reference must fail here rather than reaching the compiler.
func TestCompilableFromService_errorsOnUnknownStation(t *testing.T) {
	svc := Service{
		ID:    "svc-1",
		Stops: []ServiceStop{{StationID: "st-a", Sequence: 1}, {StationID: "st-nope", Sequence: 2}},
	}

	if _, err := CompilableFromService(adapterRoute(), adapterStations(), svc, physicsTestVehicle()); err == nil {
		t.Error("CompilableFromService() error = nil, want an error for an unknown station id")
	}
}

// The user adapter's whole job: embedded points already carry their position,
// so it only has to mint an identity and apply the flat dwell.
func TestCompilableFromUserService_projectsEmbeddedStops(t *testing.T) {
	svc := UserService{
		ID:      "us-1",
		Slug:    "my-express",
		RouteID: "rt-1",
		Vehicle: VehicleParams{MaxSpeedKMH: 200, AccelerationMS2: 1, DecelerationMS2: 1, DwellS: 45},
		Stops: []ServiceStopPoint{
			{Name: "North End", Lat: 0, Lng: 0, Seq: 0},
			{Name: "South End", Lat: 0, Lng: 1, Seq: 1},
		},
		FrequencyWindows: []FrequencyWindow{{StartTime: "06:00", EndTime: "09:00", HeadwayS: 300}},
	}

	got := CompilableFromUserService(adapterRoute(), svc)

	if got.ID != "us-1" {
		t.Errorf("ID = %q, want us-1", got.ID)
	}
	if got.Vehicle != svc.Vehicle {
		t.Errorf("Vehicle = %+v, want the service's inline params verbatim", got.Vehicle)
	}
	want := []CompiledStop{
		{Slug: "my-express--north-end", Lat: 0, Lng: 0, DwellS: 45},
		{Slug: "my-express--south-end", Lat: 0, Lng: 1, DwellS: 45},
	}
	if len(got.Stops) != len(want) {
		t.Fatalf("len(Stops) = %d, want %d", len(got.Stops), len(want))
	}
	for i, w := range want {
		if got.Stops[i] != w {
			t.Errorf("Stops[%d] = %+v, want %+v", i, got.Stops[i], w)
		}
	}
}

// Stop slugs are namespaced by the owning service, so the same stop name in two
// services stays distinct in a graph assembled from both.
func TestCompilableFromUserService_namespacesStopSlugsPerService(t *testing.T) {
	stops := []ServiceStopPoint{{Name: "Downtown", Seq: 0}, {Name: "Airport", Seq: 1}}
	a := CompilableFromUserService(adapterRoute(), UserService{Slug: "line-a", Stops: stops})
	b := CompilableFromUserService(adapterRoute(), UserService{Slug: "line-b", Stops: stops})

	if a.Stops[0].Slug == b.Stops[0].Slug {
		t.Errorf("both services minted %q for Downtown, want service-namespaced slugs", a.Stops[0].Slug)
	}
	if a.Stops[0].Slug != "line-a--downtown" {
		t.Errorf("a.Stops[0].Slug = %q, want line-a--downtown", a.Stops[0].Slug)
	}
}

// Two stops named the same within one service must still be distinguishable,
// or their graph edges would collapse into each other.
func TestCompilableFromUserService_disambiguatesDuplicateStopNames(t *testing.T) {
	svc := UserService{
		Slug: "loop",
		Stops: []ServiceStopPoint{
			{Name: "Central", Lat: 0, Lng: 0, Seq: 0},
			{Name: "Midway", Lat: 0, Lng: 0.5, Seq: 1},
			{Name: "Central", Lat: 0, Lng: 1, Seq: 2},
		},
	}

	got := CompilableFromUserService(adapterRoute(), svc)

	seen := make(map[string]bool, len(got.Stops))
	for _, stop := range got.Stops {
		if seen[stop.Slug] {
			t.Errorf("duplicate stop slug %q", stop.Slug)
		}
		seen[stop.Slug] = true
	}
	if got.Stops[0].Slug != "loop--central" {
		t.Errorf("Stops[0].Slug = %q, want the first Central to keep the plain slug", got.Stops[0].Slug)
	}
}

// Seq, not slice order, is the stopping pattern — same contract as the seeded
// adapter's Sequence handling.
func TestCompilableFromUserService_ordersStopsBySeq(t *testing.T) {
	svc := UserService{
		Slug: "line",
		Stops: []ServiceStopPoint{
			{Name: "Last", Lat: 0, Lng: 1, Seq: 1},
			{Name: "First", Lat: 0, Lng: 0, Seq: 0},
		},
	}

	got := CompilableFromUserService(adapterRoute(), svc)

	if got.Stops[0].Slug != "line--first" || got.Stops[1].Slug != "line--last" {
		t.Errorf("stop slugs = %q, %q, want line--first, line--last in Seq order",
			got.Stops[0].Slug, got.Stops[1].Slug)
	}
}

// The point of the whole refactor: both models reach the one compiler. A user
// service and the seeded service describing the same two stops on the same
// route with the same kinematics must compile to the same run times.
func TestCompileServicePhysics_bothAdaptersAgreeOnRunTime(t *testing.T) {
	dwell := 0
	seededSvc := Service{
		ID:     "svc-1",
		Active: true,
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1, DwellS: &dwell},
			{StationID: "st-b", Sequence: 2, DwellS: &dwell},
		},
	}
	seeded, err := CompilableFromService(adapterRoute(), adapterStations(), seededSvc, physicsTestVehicle())
	if err != nil {
		t.Fatalf("CompilableFromService() error = %v, want nil", err)
	}

	vt := physicsTestVehicle()
	userSvc := UserService{
		ID:   "us-1",
		Slug: "us",
		Vehicle: VehicleParams{
			MaxSpeedKMH:     vt.MaxSpeedKMH,
			AccelerationMS2: vt.AccelerationMS2,
			DecelerationMS2: vt.DecelerationMS2,
			DwellS:          0,
		},
		Stops: []ServiceStopPoint{
			{Name: "A", Lat: 0, Lng: 0, Seq: 0},
			{Name: "B", Lat: 0, Lng: 1, Seq: 1},
		},
	}
	authored := CompilableFromUserService(adapterRoute(), userSvc)

	seededGraph, err := CompileServicePhysics(seeded)
	if err != nil {
		t.Fatalf("CompileServicePhysics(seeded) error = %v, want nil", err)
	}
	authoredGraph, err := CompileServicePhysics(authored)
	if err != nil {
		t.Fatalf("CompileServicePhysics(authored) error = %v, want nil", err)
	}

	if len(seededGraph.Edges) != len(authoredGraph.Edges) {
		t.Fatalf("edge counts differ: seeded %d, authored %d",
			len(seededGraph.Edges), len(authoredGraph.Edges))
	}
	for i := range seededGraph.Edges {
		if seededGraph.Edges[i].Seconds != authoredGraph.Edges[i].Seconds {
			t.Errorf("edge %d: seeded %ds, authored %ds — the two models must compile identically",
				i, seededGraph.Edges[i].Seconds, authoredGraph.Edges[i].Seconds)
		}
	}
}
