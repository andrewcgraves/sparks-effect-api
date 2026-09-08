package transit

import (
	"math"
	"testing"
)

// SPA-264 gives every edge the corridor its hop runs over and the chainage of
// its two endpoint stations along that corridor's alignment, so a consumer can
// draw a partial leg without reconstructing the geometry itself.
//
// The three fields travel together: an edge either names a route and carries
// both chainages, or names no route and carries neither. That is the same shape
// a graph compiled before this change has, which is why nothing downstream
// needs a second code path to recognise a hop it cannot draw.

// edgeByHop indexes a service graph's directed edges by their endpoints, which
// is how every assertion below reads one back.
func edgeByHop(t *testing.T, sg ServiceGraph, from, to string) Edge {
	t.Helper()
	for _, e := range sg.Edges {
		if e.FromSlug == from && e.ToSlug == to {
			return e
		}
	}
	t.Fatalf("no edge %s→%s in %v", from, to, sg.Edges)
	return Edge{}
}

const chainageTolM = 1e-6

func assertChainage(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > chainageTolM {
		t.Errorf("%s = %.6f, want %.6f", label, got, want)
	}
}

// The physics path knows the route outright — a CompilableService carries the
// one alignment its stops were authored against — so every edge it emits names
// it, and the chainages are the projection it already performs.
//
// The line is the equatorial (0,0)→(1,0) alignment the rest of this package's
// physics tests use, whose length in this projection is
// R * pi/180 = 111194.926644... m, so station b's chainage is the whole line.
func TestCompileServicePhysics_edgesCarryRouteAndEndpointChainage(t *testing.T) {
	route := Route{
		ID:       "rt-1",
		Slug:     "rt-1",
		Geometry: GeoLineString{Type: "LineString", Coordinates: [][]float64{{0, 0}, {1, 0}}},
	}
	stations := []Station{
		{ID: "st-a", Slug: "a", Location: GeoPoint{Coordinates: []float64{0, 0}}},
		{ID: "st-b", Slug: "b", Location: GeoPoint{Coordinates: []float64{1, 0}}},
	}
	svc := Service{ID: "svc-1", Active: true, Stops: []ServiceStop{
		{StationID: "st-a", Sequence: 1},
		{StationID: "st-b", Sequence: 2},
	}}

	got, err := compileSeeded(t, route, stations, svc, physicsTestVehicle())
	if err != nil {
		t.Fatalf("compileSeeded: %v", err)
	}

	const lineLenM = 111194.92664455873

	fwd := edgeByHop(t, got, "a", "b")
	if fwd.RouteID != "rt-1" {
		t.Errorf("a→b RouteID = %q, want %q", fwd.RouteID, "rt-1")
	}
	assertChainage(t, "a→b FromChainageM", fwd.FromChainageM, 0)
	assertChainage(t, "a→b ToChainageM", fwd.ToChainageM, lineLenM)

	// The reverse edge is the same hop walked the other way, so its chainages
	// are the same two numbers swapped — descending, which is not a special
	// case anywhere that reads them.
	rev := edgeByHop(t, got, "b", "a")
	if rev.RouteID != "rt-1" {
		t.Errorf("b→a RouteID = %q, want %q", rev.RouteID, "rt-1")
	}
	assertChainage(t, "b→a FromChainageM", rev.FromChainageM, lineLenM)
	assertChainage(t, "b→a ToChainageM", rev.ToChainageM, 0)
}

// The seeded path compiles from a calibrated run-time table rather than from
// geometry, and used to discard the routes it was handed entirely. It now snaps
// its stations onto them with the same projection the physics path uses, which
// is what lets a seeded scenario draw progress at all.
func TestCompile_seededEdgesCarryRouteAndEndpointChainage(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	route := Route{
		ID:       "rt-1",
		Slug:     "rt-1",
		Geometry: GeoLineString{Type: "LineString", Coordinates: [][]float64{{0, 0}, {1, 0}, {2, 0}}},
	}
	stations := []Station{
		{ID: "st-a", Slug: "a", Name: "A", Location: GeoPoint{Coordinates: []float64{0, 0}}},
		{ID: "st-b", Slug: "b", Name: "B", Location: GeoPoint{Coordinates: []float64{1, 0}}},
	}
	segments := TravelTimes{Segments: []SegmentTime{
		{FromSlug: "a", ToSlug: "b", RunSeconds: 600, RouteID: "rt-1"},
	}}
	services := []Service{{
		ID: "svc-1", Active: true, VehicleTypeID: "vt-1",
		Stops: []ServiceStop{{StationID: "st-a", Sequence: 1}, {StationID: "st-b", Sequence: 2}},
	}}

	g, err := Compile(sc, []Route{route}, stations, services, []VehicleType{testVehicle()}, segments, DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	const halfLineM = 111194.92664455873

	fwd := edgeByHop(t, g.Services[0], "a", "b")
	if fwd.RouteID != "rt-1" {
		t.Errorf("a→b RouteID = %q, want %q", fwd.RouteID, "rt-1")
	}
	assertChainage(t, "a→b FromChainageM", fwd.FromChainageM, 0)
	assertChainage(t, "a→b ToChainageM", fwd.ToChainageM, halfLineM)

	rev := edgeByHop(t, g.Services[0], "b", "a")
	assertChainage(t, "b→a FromChainageM", rev.FromChainageM, halfLineM)
	assertChainage(t, "b→a ToChainageM", rev.ToChainageM, 0)
}

// Compilation is route-blind: a seeded service may path across two corridors
// that meet at a shared station, and the segment rows say which corridor each
// hop runs over. A single route per service would be wrong for exactly those
// services, and silently, so the route is settled per edge from the segments
// the hop actually traverses.
func TestCompile_seededServiceSpanningTwoCorridorsGetsRoutePerEdge(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	north := Route{
		ID:       "rt-north",
		Slug:     "rt-north",
		Geometry: GeoLineString{Type: "LineString", Coordinates: [][]float64{{0, 0}, {0, 1}}},
	}
	east := Route{
		ID:       "rt-east",
		Slug:     "rt-east",
		Geometry: GeoLineString{Type: "LineString", Coordinates: [][]float64{{0, 1}, {1, 1}}},
	}
	stations := []Station{
		{ID: "st-a", Slug: "a", Name: "A", Location: GeoPoint{Coordinates: []float64{0, 0}}},
		{ID: "st-b", Slug: "b", Name: "B", Location: GeoPoint{Coordinates: []float64{0, 1}}},
		{ID: "st-c", Slug: "c", Name: "C", Location: GeoPoint{Coordinates: []float64{1, 1}}},
	}
	segments := TravelTimes{Segments: []SegmentTime{
		{FromSlug: "a", ToSlug: "b", RunSeconds: 600, RouteID: "rt-north"},
		{FromSlug: "b", ToSlug: "c", RunSeconds: 600, RouteID: "rt-east"},
	}}
	services := []Service{{
		ID: "svc-1", Active: true, VehicleTypeID: "vt-1",
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
			{StationID: "st-c", Sequence: 3},
		},
	}}

	g, err := Compile(sc, []Route{north, east}, stations, services, []VehicleType{testVehicle()}, segments, DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got := edgeByHop(t, g.Services[0], "a", "b").RouteID; got != "rt-north" {
		t.Errorf("a→b RouteID = %q, want rt-north", got)
	}
	if got := edgeByHop(t, g.Services[0], "b", "c").RouteID; got != "rt-east" {
		t.Errorf("b→c RouteID = %q, want rt-east", got)
	}
	// b sits at the start of the east alignment and the end of the north one,
	// so the same station has two chainages — which is why chainage is stored
	// per edge alongside the route it is measured against, never per node.
	assertChainage(t, "b→c FromChainageM", edgeByHop(t, g.Services[0], "b", "c").FromChainageM, 0)
}

// A station too far from the alignment its hop runs over cannot be placed on
// it, and a stub drawn from a guessed position would be worse than no stub. The
// edge is emitted with no route and no chainage — the shape a pre-SPA-264 graph
// has — rather than the compile failing, because progress is decoration and a
// seed correction must never be able to take the isochrone down with it.
func TestCompile_seededStationOffItsRouteYieldsEdgeWithoutRouteOrChainage(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	// The alignment runs due east along the equator; b sits a degree north of
	// it, roughly 111 km off, far outside OffRouteThresholdM.
	route := Route{
		ID:       "rt-1",
		Slug:     "rt-1",
		Geometry: GeoLineString{Type: "LineString", Coordinates: [][]float64{{0, 0}, {1, 0}}},
	}
	stations := []Station{
		{ID: "st-a", Slug: "a", Name: "A", Location: GeoPoint{Coordinates: []float64{0, 0}}},
		{ID: "st-b", Slug: "b", Name: "B", Location: GeoPoint{Coordinates: []float64{1, 1}}},
	}
	segments := TravelTimes{Segments: []SegmentTime{
		{FromSlug: "a", ToSlug: "b", RunSeconds: 600, RouteID: "rt-1"},
	}}
	services := []Service{{
		ID: "svc-1", Active: true, VehicleTypeID: "vt-1",
		Stops: []ServiceStop{{StationID: "st-a", Sequence: 1}, {StationID: "st-b", Sequence: 2}},
	}}

	g, err := Compile(sc, []Route{route}, stations, services, []VehicleType{testVehicle()}, segments, DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("Compile: %v, want a graph with an unplaceable hop rather than an error", err)
	}
	for _, e := range g.Services[0].Edges {
		if e.RouteID != "" {
			t.Errorf("%s→%s RouteID = %q, want empty (b cannot be placed on rt-1)", e.FromSlug, e.ToSlug, e.RouteID)
		}
		if e.FromChainageM != 0 || e.ToChainageM != 0 {
			t.Errorf("%s→%s chainages = (%v, %v), want both zero alongside an absent route",
				e.FromSlug, e.ToSlug, e.FromChainageM, e.ToChainageM)
		}
	}
	// The hop is still a hop: only the decoration is missing. 600 s of run time
	// plus b's dwell, which is DwellStepS since neither station declares a
	// platform height matching the vehicle's floor.
	if got := edgeByHop(t, g.Services[0], "a", "b").Seconds; got != 600+180 {
		t.Errorf("a→b Seconds = %d, want %d — an unplaceable stop must not change the graph's weights", got, 600+180)
	}
}

// A hop whose segments disagree about the corridor has no single alignment to
// measure against. It degrades the same way an unplaceable station does rather
// than picking one of them, which would draw a stub down a corridor the leg
// only half runs over.
func TestCompile_seededMultiSegmentHopAcrossCorridorsHasNoRoute(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	north := Route{ID: "rt-north", Slug: "rt-north",
		Geometry: GeoLineString{Type: "LineString", Coordinates: [][]float64{{0, 0}, {0, 1}}}}
	east := Route{ID: "rt-east", Slug: "rt-east",
		Geometry: GeoLineString{Type: "LineString", Coordinates: [][]float64{{0, 1}, {1, 1}}}}
	stations := []Station{
		{ID: "st-a", Slug: "a", Name: "A", Location: GeoPoint{Coordinates: []float64{0, 0}}},
		{ID: "st-b", Slug: "b", Name: "B", Location: GeoPoint{Coordinates: []float64{0, 1}}},
		{ID: "st-c", Slug: "c", Name: "C", Location: GeoPoint{Coordinates: []float64{1, 1}}},
	}
	segments := TravelTimes{Segments: []SegmentTime{
		{FromSlug: "a", ToSlug: "b", RunSeconds: 600, RouteID: "rt-north"},
		{FromSlug: "b", ToSlug: "c", RunSeconds: 600, RouteID: "rt-east"},
	}}
	// The service runs a→c non-stop, so the one hop covers both corridors.
	services := []Service{{
		ID: "svc-1", Active: true, VehicleTypeID: "vt-1",
		Stops: []ServiceStop{{StationID: "st-a", Sequence: 1}, {StationID: "st-c", Sequence: 2}},
	}}

	g, err := Compile(sc, []Route{north, east}, stations, services, []VehicleType{testVehicle()}, segments, DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got := edgeByHop(t, g.Services[0], "a", "c").RouteID; got != "" {
		t.Errorf("a→c RouteID = %q, want empty — the hop spans two corridors", got)
	}
}

// A seeded scenario compiled with no routes in hand — every existing caller
// passed nil before SPA-264 — still compiles, and simply carries no progress
// data. The degradation rule covers it with no second code path.
func TestCompile_seededWithoutRoutesStillCompiles(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	services := []Service{{
		ID: "svc-local", Active: true, VehicleTypeID: "vt-1",
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
		},
	}}

	g, err := Compile(sc, nil, testStations(), services, []VehicleType{testVehicle()}, testSegments(), DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for _, e := range g.Services[0].Edges {
		if e.RouteID != "" {
			t.Errorf("%s→%s RouteID = %q, want empty with no routes supplied", e.FromSlug, e.ToSlug, e.RouteID)
		}
	}
}

// The flagship seeded scenario is where this feature is actually seen, on the
// public scenario page's curated illustrations, so its edges being placeable is
// the fact stages 2-4 rest on. Nothing else in this package would notice the
// seed drifting off its alignments: the tolerate-and-omit rule above means the
// compile would keep succeeding and every stub would silently stop being drawn.
func TestCompile_seededFlagshipScenarioEdgesAreAllPlaced(t *testing.T) {
	store := mustNewStore(t)
	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr not found")
	}

	g, err := Compile(
		sc,
		store.GetRoutesByScenario(sc.ID),
		store.GetStationsByScenario(sc.ID),
		store.GetServicesByScenario(sc.ID),
		append([]VehicleType(nil), store.vehicleTypes...),
		mustTravelTimes(t, store, "ca-hsr"),
		DefaultBoardingWaitPolicy(),
	)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	edges := 0
	for _, sg := range g.Services {
		for _, e := range sg.Edges {
			edges++
			if e.RouteID == "" {
				t.Errorf("%s: edge %s→%s has no route id — its stations no longer place on their alignment",
					sg.ServiceID, e.FromSlug, e.ToSlug)
				continue
			}
			if e.FromChainageM == e.ToChainageM {
				t.Errorf("%s: edge %s→%s spans zero chainage on %s",
					sg.ServiceID, e.FromSlug, e.ToSlug, e.RouteID)
			}
		}
	}
	if edges == 0 {
		t.Fatal("the flagship scenario compiled to no edges at all")
	}
}
