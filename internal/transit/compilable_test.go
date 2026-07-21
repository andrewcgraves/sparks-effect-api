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

// mustCompilableFromUserService keeps each user-adapter test to the assertion it
// is actually about; the adapter's error paths have tests of their own.
func mustCompilableFromUserService(t *testing.T, route Route, svc UserService) CompilableService {
	t.Helper()
	cs, err := CompilableFromUserService(route, svc)
	if err != nil {
		t.Fatalf("CompilableFromUserService() error = %v, want nil", err)
	}
	return cs
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

	want := []CompilableStop{
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

// Turning a station into a position is the adapter's job, so a station carrying
// no coordinates has to fail here — it used to reach the compiler and panic on
// an out-of-range index.
func TestCompilableFromService_errorsOnStationWithoutLocation(t *testing.T) {
	stations := []Station{
		{ID: "st-a", Slug: "a", Location: GeoPoint{Coordinates: []float64{0, 0}}},
		{ID: "st-b", Slug: "b"},
	}
	svc := Service{
		ID:    "svc-1",
		Stops: []ServiceStop{{StationID: "st-a", Sequence: 1}, {StationID: "st-b", Sequence: 2}},
	}

	if _, err := CompilableFromService(adapterRoute(), stations, svc, physicsTestVehicle()); err == nil {
		t.Error("CompilableFromService() error = nil, want an error for a station with no location")
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

	got := mustCompilableFromUserService(t, adapterRoute(), svc)

	if got.ID != "us-1" {
		t.Errorf("ID = %q, want us-1", got.ID)
	}
	wantVehicle := Kinematics{MaxSpeedKMH: 200, AccelerationMS2: 1, DecelerationMS2: 1}
	if got.Vehicle != wantVehicle {
		t.Errorf("Vehicle = %+v, want %+v — the inline params' kinematics, without DwellS", got.Vehicle, wantVehicle)
	}
	want := []CompilableStop{
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
	a := mustCompilableFromUserService(t, adapterRoute(), UserService{Slug: "line-a", RouteID: "rt-1", Stops: stops})
	b := mustCompilableFromUserService(t, adapterRoute(), UserService{Slug: "line-b", RouteID: "rt-1", Stops: stops})

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
		Slug:    "loop",
		RouteID: "rt-1",
		Stops: []ServiceStopPoint{
			{Name: "Central", Lat: 0, Lng: 0, Seq: 0},
			{Name: "Midway", Lat: 0, Lng: 0.5, Seq: 1},
			{Name: "Central", Lat: 0, Lng: 1, Seq: 2},
		},
	}

	got := mustCompilableFromUserService(t, adapterRoute(), svc)

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
		Slug:    "line",
		RouteID: "rt-1",
		Stops: []ServiceStopPoint{
			{Name: "Last", Lat: 0, Lng: 1, Seq: 1},
			{Name: "First", Lat: 0, Lng: 0, Seq: 0},
		},
	}

	got := mustCompilableFromUserService(t, adapterRoute(), svc)

	if got.Stops[0].Slug != "line--first" || got.Stops[1].Slug != "line--last" {
		t.Errorf("stop slugs = %q, %q, want line--first, line--last in Seq order",
			got.Stops[0].Slug, got.Stops[1].Slug)
	}
}

// The defect StopSlugs exists to prevent: a persistence path deriving a stop's
// slug on its own disagrees with the compiler the moment a name repeats, and
// SPA-103 would then store node keys naming the wrong stop. One minter, so the
// two cannot drift.
func TestStopSlugs_matchTheSlugsTheAdapterMints(t *testing.T) {
	// "Central 2" is the trap: it collides with the disambiguated form of a
	// repeated "Central", so a per-name minter hands out a slug the compiler
	// gave to a different stop entirely.
	svc := UserService{
		Slug:    "loop",
		RouteID: "rt-1",
		Stops: []ServiceStopPoint{
			{Name: "Central", Lat: 0, Lng: 0, Seq: 0},
			{Name: "Central", Lat: 0, Lng: 0.5, Seq: 1},
			{Name: "Central 2", Lat: 0, Lng: 1, Seq: 2},
		},
	}

	got := mustCompilableFromUserService(t, adapterRoute(), svc)
	slugs := StopSlugs(svc)

	if len(slugs) != len(svc.Stops) {
		t.Fatalf("len(StopSlugs()) = %d, want one per stop (%d)", len(slugs), len(svc.Stops))
	}
	// Seq matches slice order here, so the compiled stops line up with StopSlugs.
	for i, stop := range got.Stops {
		if stop.Slug != slugs[i] {
			t.Errorf("compiled stop %d has slug %q but StopSlugs() says %q — the two must not drift",
				i, stop.Slug, slugs[i])
		}
	}

	seen := make(map[string]bool, len(slugs))
	for _, s := range slugs {
		if seen[s] {
			t.Errorf("StopSlugs() returned duplicate slug %q", s)
		}
		seen[s] = true
	}
}

// StopSlugs is aligned with svc.Stops as authored rather than with the compiled
// stopping order, so a caller holding a stop can index straight into it.
func TestStopSlugs_alignWithSliceOrderNotSeq(t *testing.T) {
	svc := UserService{
		Slug:    "line",
		RouteID: "rt-1",
		Stops: []ServiceStopPoint{
			{Name: "Last", Lat: 0, Lng: 1, Seq: 1},
			{Name: "First", Lat: 0, Lng: 0, Seq: 0},
		},
	}

	slugs := StopSlugs(svc)
	if slugs[0] != "line--last" || slugs[1] != "line--first" {
		t.Errorf("StopSlugs() = %v, want slice order (line--last, line--first)", slugs)
	}

	// Only the identities follow authoring order; the compiler still consumes
	// the stops in Seq order.
	got := mustCompilableFromUserService(t, adapterRoute(), svc)
	if got.Stops[0].Slug != "line--first" || got.Stops[1].Slug != "line--last" {
		t.Errorf("compiled slugs = %q, %q, want line--first, line--last in Seq order",
			got.Stops[0].Slug, got.Stops[1].Slug)
	}
}

// Compiling against the wrong alignment would project the stops onto a line they
// were never authored against and produce a plausible-looking wrong graph, so
// the mismatch has to be an error rather than a silent compile.
func TestCompilableFromUserService_errorsOnRouteMismatch(t *testing.T) {
	svc := UserService{
		ID:      "us-1",
		Slug:    "us",
		RouteID: "rt-other",
		Stops:   []ServiceStopPoint{{Name: "A", Seq: 0}, {Name: "B", Lng: 1, Seq: 1}},
	}

	if _, err := CompilableFromUserService(adapterRoute(), svc); err == nil {
		t.Error("CompilableFromUserService() error = nil, want an error when route is not the one the service references")
	}
}

// The point of the whole refactor: both models reach the one compiler. A user
// service and the seeded service describing the same two stops on the same
// route with the same kinematics must compile to the same edge times.
//
// Dwell is deliberately non-zero and equal on both sides, so this pins the two
// adapters' dwell resolution as well as their run times — the part most likely
// to drift, since the seeded arm reaches its number through a per-stop override
// and the authored arm through the flat VehicleParams.DwellS.
func TestCompileServicePhysics_bothAdaptersAgreeOnRunTime(t *testing.T) {
	dwell := 45
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
		ID:      "us-1",
		Slug:    "us",
		RouteID: "rt-1",
		Vehicle: VehicleParams{
			MaxSpeedKMH:     vt.MaxSpeedKMH,
			AccelerationMS2: vt.AccelerationMS2,
			DecelerationMS2: vt.DecelerationMS2,
			DwellS:          dwell,
		},
		Stops: []ServiceStopPoint{
			{Name: "A", Lat: 0, Lng: 0, Seq: 0},
			{Name: "B", Lat: 0, Lng: 1, Seq: 1},
		},
	}
	authored := mustCompilableFromUserService(t, adapterRoute(), userSvc)

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
