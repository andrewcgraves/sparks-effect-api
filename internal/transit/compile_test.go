package transit

import (
	"fmt"
	"strings"
	"testing"
)

func testStations() []Station {
	return []Station{
		{ID: "st-a", Slug: "a", Name: "A"},
		{ID: "st-b", Slug: "b", Name: "B"},
		{ID: "st-c", Slug: "c", Name: "C"},
	}
}

func testSegments() TravelTimes {
	return TravelTimes{
		ScenarioSlug: "test",
		Segments: []SegmentTime{
			{FromSlug: "a", ToSlug: "b", Minutes: 10},
			{FromSlug: "b", ToSlug: "c", Minutes: 20},
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
	stations := []Station{
		{ID: "st-a", Slug: "a", PlatformHeight: "high"},
		{ID: "st-b", Slug: "b", PlatformHeight: "high"},
		{ID: "st-c", Slug: "c", PlatformHeight: "high"},
	}
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

func TestCompile_expressSkipsIntermediateDwell(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	stations := []Station{
		{ID: "st-a", Slug: "a", PlatformHeight: "high"},
		{ID: "st-b", Slug: "b", PlatformHeight: "high"},
		{ID: "st-c", Slug: "c", PlatformHeight: "high"},
	}
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
	stations := []Station{
		{ID: "st-a", Slug: "a", PlatformHeight: "high"},
		{ID: "st-b", Slug: "b", PlatformHeight: "low"},
		{ID: "st-c", Slug: "c", PlatformHeight: "high"},
	}
	segments := TravelTimes{Segments: []SegmentTime{
		{FromSlug: "a", ToSlug: "b", Minutes: 1},
		{FromSlug: "b", ToSlug: "c", Minutes: 1},
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
		Segments: []SegmentTime{{FromSlug: "a", ToSlug: "missing", Minutes: 5}},
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
			windows[i] = FrequencyWindow{ID: fmt.Sprintf("fw-%d", i), ServiceID: id, HeadwayS: h}
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
	if len(g.Services) != 2 {
		t.Fatalf("want 2 service graphs (Express + Local), got %d", len(g.Services))
	}
}
