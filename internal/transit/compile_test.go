package transit

import (
	"strings"
	"testing"
)

// Stations carry coordinates because a compiled graph carries its own geometry
// (TransitGraph.Nodes) — a station with no usable location fails the compile.
func testStations() []Station {
	return []Station{
		{ID: "st-a", Slug: "a", Name: "A", Location: GeoPoint{Coordinates: []float64{-122.4, 37.7}}},
		{ID: "st-b", Slug: "b", Name: "B", Location: GeoPoint{Coordinates: []float64{-122.5, 37.8}}},
		{ID: "st-c", Slug: "c", Name: "C", Location: GeoPoint{Coordinates: []float64{-122.6, 37.9}}},
	}
}

// platformStations is testStations with the platform heights a dwell test
// varies. The coordinates come along because a compile needs them, not because
// these tests care about position.
func platformStations(heights ...string) []Station {
	sts := testStations()
	for i, h := range heights {
		sts[i].PlatformHeight = h
	}
	return sts
}

func testSegments() TravelTimes {
	return TravelTimes{
		ScenarioSlug: "test",
		Segments: []SegmentTime{
			{FromSlug: "a", ToSlug: "b", RunSeconds: 600},
			{FromSlug: "b", ToSlug: "c", RunSeconds: 1200},
		},
	}
}

func testVehicle() VehicleType {
	return VehicleType{
		ID:          "vt-1",
		FloorHeight: "high",
		DwellLevelS: 90,
		DwellStepS:  180,
	}
}

func TestCompile_createsServiceGraphsWithEdges(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	services := []Service{
		{
			ID:            "svc-local",
			Active:        true,
			Name:          "Local",
			VehicleTypeID: "vt-1",
			Stops: []ServiceStop{
				{StationID: "st-a", Sequence: 1},
				{StationID: "st-b", Sequence: 2},
				{StationID: "st-c", Sequence: 3},
			},
		},
		{
			ID:     "svc-inactive",
			Active: false,
			Stops:  []ServiceStop{{StationID: "st-a", Sequence: 1}},
		},
	}

	g, err := Compile(sc, nil, testStations(), services, []VehicleType{testVehicle()}, testSegments())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(g.Services) != 1 {
		t.Fatalf("want 1 active ServiceGraph, got %d", len(g.Services))
	}
	sg := g.Services[0]
	if sg.ServiceID != "svc-local" {
		t.Errorf("ServiceID: want svc-local, got %s", sg.ServiceID)
	}
	if sg.WaitSecs != 0 {
		t.Errorf("WaitSecs: want 0 (M2 wires wait), got %d", sg.WaitSecs)
	}
	if len(sg.Edges) != 4 {
		t.Fatalf("want 4 directed edges (2 hops × both dirs), got %d: %v", len(sg.Edges), sg.Edges)
	}
}

func TestCompile_edgeSecondsIncludeRunAndDwell(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	stations := platformStations("high", "high", "high")
	services := []Service{{
		ID:            "svc-local",
		Active:        true,
		VehicleTypeID: "vt-1",
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
			{StationID: "st-c", Sequence: 3},
		},
	}}

	g, err := Compile(sc, nil, stations, services, []VehicleType{testVehicle()}, testSegments())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	byKey := map[string]int{}
	for _, e := range g.Services[0].Edges {
		byKey[e.FromSlug+"→"+e.ToSlug] = e.Seconds
	}
	// a→b: 10m*60 + dwell(b)=90
	if got := byKey["a→b"]; got != 10*60+90 {
		t.Errorf("a→b: want %d, got %d", 10*60+90, got)
	}
	// b→c: 20m*60 + dwell(c)=90
	if got := byKey["b→c"]; got != 20*60+90 {
		t.Errorf("b→c: want %d, got %d", 20*60+90, got)
	}
}

// Dwell is reported alongside the edge total rather than only folded into it,
// so a consumer can say how long the vehicle stands at the destination. The
// total is unchanged and still includes it.
func TestCompile_edgesReportDwellAlongsideTotal(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	override := 30
	stations := platformStations("high", "low", "high")
	segments := TravelTimes{Segments: []SegmentTime{
		{FromSlug: "a", ToSlug: "b", RunSeconds: 60},
		{FromSlug: "b", ToSlug: "c", RunSeconds: 60},
	}}
	services := []Service{{
		ID:            "svc-1",
		Active:        true,
		VehicleTypeID: "vt-1",
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
			{StationID: "st-c", Sequence: 3, DwellS: &override},
		},
	}}

	g, err := Compile(sc, nil, stations, services, []VehicleType{testVehicle()}, segments)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	byKey := map[string]Edge{}
	for _, e := range g.Services[0].Edges {
		byKey[e.FromSlug+"→"+e.ToSlug] = e
	}
	// a→b arrives at b, which dwells the step time (platform heights differ).
	if got := byKey["a→b"]; got.DwellS != 180 || got.Seconds != 60+180 {
		t.Errorf("a→b: want DwellS 180 and Seconds %d, got DwellS %d and Seconds %d",
			60+180, got.DwellS, got.Seconds)
	}
	// b→c arrives at c, whose stop overrides dwell to 30.
	if got := byKey["b→c"]; got.DwellS != 30 || got.Seconds != 60+30 {
		t.Errorf("b→c: want DwellS 30 and Seconds %d, got DwellS %d and Seconds %d",
			60+30, got.DwellS, got.Seconds)
	}
}

func TestCompile_expressSkipsIntermediateDwell(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	stations := platformStations("high", "high", "high")
	express := Service{
		ID:            "svc-express",
		Active:        true,
		VehicleTypeID: "vt-1",
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-c", Sequence: 2},
		},
	}
	local := Service{
		ID:            "svc-local",
		Active:        true,
		VehicleTypeID: "vt-1",
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
			{StationID: "st-c", Sequence: 3},
		},
	}

	g, err := Compile(sc, nil, stations, []Service{express, local}, []VehicleType{testVehicle()}, testSegments())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	var expressAC, localAC int
	for _, sg := range g.Services {
		secs := 0
		for _, e := range sg.Edges {
			if e.FromSlug == "a" && (e.ToSlug == "b" || e.ToSlug == "c") {
				secs += e.Seconds
			}
			if e.FromSlug == "b" && e.ToSlug == "c" {
				secs += e.Seconds
			}
		}
		if sg.ServiceID == "svc-express" {
			for _, e := range sg.Edges {
				if e.FromSlug == "a" && e.ToSlug == "c" {
					expressAC = e.Seconds
				}
			}
		}
		if sg.ServiceID == "svc-local" {
			localAC = secs
		}
	}
	// express a→c: (10+20)*60 + dwell(c)=90, no dwell at skipped b
	wantExpress := (10+20)*60 + 90
	if expressAC != wantExpress {
		t.Errorf("express a→c: want %d, got %d", wantExpress, expressAC)
	}
	// local a→b→c: 10*60+90 + 20*60+90
	wantLocal := 10*60 + 90 + 20*60 + 90
	if localAC != wantLocal {
		t.Errorf("local a→c via b: want %d, got %d", wantLocal, localAC)
	}
	if localAC-expressAC != 90 {
		t.Errorf("local−express delta: want skipped dwell 90, got %d", localAC-expressAC)
	}
}

func TestCompile_dwellResolution(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	override := 30
	stations := platformStations("high", "low", "high")
	segments := TravelTimes{Segments: []SegmentTime{
		{FromSlug: "a", ToSlug: "b", RunSeconds: 60},
		{FromSlug: "b", ToSlug: "c", RunSeconds: 60},
	}}
	vt := testVehicle()
	services := []Service{{
		ID:            "svc-1",
		Active:        true,
		VehicleTypeID: "vt-1",
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
			{StationID: "st-c", Sequence: 3, DwellS: &override},
		},
	}}

	g, err := Compile(sc, nil, stations, services, []VehicleType{vt}, segments)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	byKey := map[string]int{}
	for _, e := range g.Services[0].Edges {
		byKey[e.FromSlug+"→"+e.ToSlug] = e.Seconds
	}
	// a→b: step dwell (low≠high) = 180
	if got := byKey["a→b"]; got != 60+180 {
		t.Errorf("a→b step dwell: want %d, got %d", 60+180, got)
	}
	// b→c: override 30
	if got := byKey["b→c"]; got != 60+30 {
		t.Errorf("b→c override dwell: want %d, got %d", 60+30, got)
	}
}

func TestCompile_unknownStationSlugInSegments(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	tt := TravelTimes{
		Segments: []SegmentTime{{FromSlug: "a", ToSlug: "missing", RunSeconds: 300}},
	}
	_, err := Compile(sc, nil, testStations(), nil, nil, tt)
	if err == nil {
		t.Fatal("expected error for unknown segment station slug")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should mention unknown slug, got: %v", err)
	}
}

func TestCompile_unknownServiceStopStation(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	services := []Service{{
		ID:            "svc-1",
		Active:        true,
		VehicleTypeID: "vt-1",
		Stops:         []ServiceStop{{StationID: "st-unknown", Sequence: 1}},
	}}
	_, err := Compile(sc, nil, testStations(), services, []VehicleType{testVehicle()}, testSegments())
	if err == nil {
		t.Fatal("expected error for unknown service stop station")
	}
	if !strings.Contains(err.Error(), "st-unknown") {
		t.Errorf("error should mention station id, got: %v", err)
	}
}

func TestCompile_serviceStopNotOnSegmentPath(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	stations := append(testStations(), Station{ID: "st-orphan", Slug: "orphan", Name: "Orphan"})
	services := []Service{{
		ID:            "svc-1",
		Active:        true,
		VehicleTypeID: "vt-1",
		Stops:         []ServiceStop{{StationID: "st-orphan", Sequence: 1}},
	}}
	_, err := Compile(sc, nil, stations, services, []VehicleType{testVehicle()}, testSegments())
	if err == nil {
		t.Fatal("expected error for service stop not on segment path")
	}
	if !strings.Contains(err.Error(), "orphan") {
		t.Errorf("error should mention orphan slug, got: %v", err)
	}
}

func TestCompile_waitSecsFromFrequencyWindows(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	makeService := func(id string, headways []int) Service {
		windows := make([]FrequencyWindow, len(headways))
		for i, h := range headways {
			windows[i] = FrequencyWindow{HeadwayS: h}
		}
		return Service{
			ID:            id,
			Active:        true,
			VehicleTypeID: "vt-1",
			Stops: []ServiceStop{
				{StationID: "st-a", Sequence: 1},
				{StationID: "st-b", Sequence: 2},
			},
			FrequencyWindows: windows,
		}
	}

	services := []Service{
		makeService("svc-one", []int{1800}),
		makeService("svc-multi", []int{1800, 3600}),
	}

	g, err := Compile(sc, nil, testStations(), services, []VehicleType{testVehicle()}, testSegments())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	byID := map[string]ServiceGraph{}
	for _, sg := range g.Services {
		byID[sg.ServiceID] = sg
	}
	if got := byID["svc-one"].WaitSecs; got != 900 {
		t.Errorf("svc-one WaitSecs: want 900, got %d", got)
	}
	if got := byID["svc-multi"].WaitSecs; got != 900 {
		t.Errorf("svc-multi WaitSecs (best/peak headway): want 900, got %d", got)
	}
}

func TestNewStore_holdsCompiledGraph(t *testing.T) {
	store := mustNewStore(t)
	g, ok := store.Graph("ca-hsr")
	if !ok {
		t.Fatal("expected compiled TransitGraph for ca-hsr")
	}
	// One graph per active service, whatever the seed currently declares —
	// counting them against a literal only re-breaks every time a service is
	// added or parked.
	sc, _ := store.GetScenarioBySlug("ca-hsr")
	want := len(store.GetServicesByScenario(sc.ID))
	if want == 0 {
		t.Fatal("expected ca-hsr to declare at least one active service")
	}
	if len(g.Services) != want {
		t.Fatalf("want one service graph per active service (%d), got %d", want, len(g.Services))
	}
}

// A compiled graph must carry its own geometry: the seeded isochrone now reads
// its nodes off the compile job's result rather than the station rows, so a
// graph without nodes has nothing to plot from (SPA-181).
func TestCompile_emitsOneNodePerStation(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	stations := testStations()
	services := []Service{{
		ID: "svc-local", Active: true, VehicleTypeID: "vt-1",
		Stops: []ServiceStop{{StationID: "st-a", Sequence: 1}, {StationID: "st-b", Sequence: 2}},
	}}

	g, err := Compile(sc, nil, stations, services, []VehicleType{testVehicle()}, testSegments())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// Every station of the scenario is a node, including "c" which no service
	// calls at — the seeded store offered all of them as isochrone origins.
	if len(g.Nodes) != len(stations) {
		t.Fatalf("len(Nodes) = %d, want %d (one per station)", len(g.Nodes), len(stations))
	}
	byslug := make(map[string]GraphNode, len(g.Nodes))
	for _, n := range g.Nodes {
		byslug[n.Slug] = n
	}
	a, ok := byslug["a"]
	if !ok {
		t.Fatalf("no node for station a; nodes = %+v", g.Nodes)
	}
	if a.Lat != 37.7 || a.Lng != -122.4 {
		t.Errorf("node a position = (%v, %v), want (37.7, -122.4)", a.Lat, a.Lng)
	}
	if len(a.Names) != 1 || a.Names[0] != "A" {
		t.Errorf("node a Names = %v, want [A]", a.Names)
	}
}

// A station whose location is malformed must fail the compile, not quietly
// become a node at (0, 0) — that coordinate is a real place in the Gulf of
// Guinea, and it would be baked into a persisted graph and plotted from.
func TestCompile_rejectsStationWithMalformedLocation(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	stations := []Station{
		{ID: "st-a", Slug: "a", Name: "A", Location: GeoPoint{Coordinates: []float64{-122.4, 37.7}}},
		{ID: "st-b", Slug: "b", Name: "B", Location: GeoPoint{Coordinates: []float64{-122.5}}},
	}
	services := []Service{{
		ID: "svc-local", Active: true, VehicleTypeID: "vt-1",
		Stops: []ServiceStop{{StationID: "st-a", Sequence: 1}, {StationID: "st-b", Sequence: 2}},
	}}

	segments := TravelTimes{ScenarioSlug: "test", Segments: []SegmentTime{
		{FromSlug: "a", ToSlug: "b", RunSeconds: 600},
	}}

	_, err := Compile(sc, nil, stations, services, []VehicleType{testVehicle()}, segments)
	if err == nil {
		t.Fatal("Compile() error = nil, want an error for a station with no usable location")
	}
	if !strings.Contains(err.Error(), "b") {
		t.Errorf("error = %v, want it to name the offending station", err)
	}
}
