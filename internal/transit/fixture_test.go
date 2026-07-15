package transit

import (
	"strings"
	"testing"
)

func servicePathSecs(sg ServiceGraph, from, to string) (int, bool) {
	secs, _, _, ok := graphDijkstra(&TransitGraph{Services: []ServiceGraph{sg}}, from, to)
	return secs, ok
}

func TestFixture_ExpressLessThanLocal_SFToLA(t *testing.T) {
	store := mustNewStore(t)
	g, ok := store.Graph("ca-hsr")
	if !ok {
		t.Fatal("missing ca-hsr graph")
	}

	sc, _ := store.GetScenarioBySlug("ca-hsr")
	var expressID, localID string
	for _, svc := range store.GetServicesByScenario(sc.ID) {
		switch svc.Name {
		case "HSR Express":
			expressID = svc.ID
		case "HSR Local":
			localID = svc.ID
		}
	}
	if expressID == "" || localID == "" {
		t.Fatal("express/local service ids not found")
	}

	var expressSecs, localSecs int
	var expressOK, localOK bool
	for _, sg := range g.Services {
		switch sg.ServiceID {
		case expressID:
			expressSecs, expressOK = servicePathSecs(sg, "sf", "los-angeles")
		case localID:
			localSecs, localOK = servicePathSecs(sg, "sf", "los-angeles")
		}
	}
	if !expressOK || !localOK {
		t.Fatalf("path missing: expressOK=%v localOK=%v", expressOK, localOK)
	}
	if expressSecs >= localSecs {
		t.Errorf("express (%d) should be < local (%d)", expressSecs, localSecs)
	}
	vt, ok := store.GetVehicleTypeByID(store.GetServicesByScenario(sc.ID)[0].VehicleTypeID)
	if !ok {
		t.Fatal("vehicle type missing")
	}
	wantDelta := 2 * vt.DwellLevelS
	if got := localSecs - expressSecs; got != wantDelta {
		t.Errorf("local−express: want %d (2×DwellLevelS), got %d", wantDelta, got)
	}
}

func TestFixture_BranchShortestPath_DijkstraBeatsBFS(t *testing.T) {
	g := &TransitGraph{Services: []ServiceGraph{{
		ServiceID: "branch",
		Edges: []Edge{
			{FromSlug: "a", ToSlug: "b", Seconds: 100},
			{FromSlug: "a", ToSlug: "c", Seconds: 1},
			{FromSlug: "c", ToSlug: "b", Seconds: 1},
		},
	}}}

	got, _, _, ok := graphDijkstra(g, "a", "b")
	if !ok {
		t.Fatal("expected path a→b")
	}
	if got != 2 {
		t.Errorf("Dijkstra shortest a→b: want 2 (via c), got %d (BFS-first-found would be 100)", got)
	}

	union := &TransitGraph{Services: []ServiceGraph{
		{ServiceID: "slow", Edges: []Edge{{FromSlug: "a", ToSlug: "b", Seconds: 100}}},
		{ServiceID: "fast", Edges: []Edge{
			{FromSlug: "a", ToSlug: "c", Seconds: 1},
			{FromSlug: "c", ToSlug: "b", Seconds: 1},
		}},
	}}
	got, _, _, ok = graphDijkstra(union, "a", "b")
	if !ok || got != 2 {
		t.Errorf("free cross-service transfer Dijkstra: want 2, got (%d, %v)", got, ok)
	}
}

func TestFixture_DijkstraPrefersLowerTotalWithWait(t *testing.T) {
	g := &TransitGraph{Services: []ServiceGraph{
		{
			ServiceID: "frequent-long",
			WaitSecs:  60,
			Edges:     []Edge{{FromSlug: "a", ToSlug: "b", Seconds: 100}},
		},
		{
			ServiceID: "rare-short",
			WaitSecs:  600,
			Edges:     []Edge{{FromSlug: "a", ToSlug: "b", Seconds: 50}},
		},
	}}
	secs, wait, serviceID, ok := graphDijkstra(g, "a", "b")
	if !ok {
		t.Fatal("expected path")
	}
	if serviceID != "frequent-long" || secs != 100 || wait != 60 {
		t.Errorf("want frequent-long (100+60), got %s secs=%d wait=%d", serviceID, secs, wait)
	}
}

func TestFixture_CompileFailuresDescriptive(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "bad"}
	stations := testStations()
	vt := testVehicle()

	_, err := Compile(sc, nil, stations, nil, nil, TravelTimes{
		Segments: []SegmentTime{{FromSlug: "a", ToSlug: "ghost", RunSeconds: 60}},
	})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("unknown slug: want error mentioning ghost, got %v", err)
	}

	_, err = Compile(sc, nil, stations, []Service{{
		ID: "svc-1", Active: true, VehicleTypeID: "vt-1",
		Stops: []ServiceStop{{StationID: "st-a", Sequence: 1}, {StationID: "missing", Sequence: 2}},
	}}, []VehicleType{vt}, testSegments())
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unknown stop: want error mentioning missing, got %v", err)
	}

	orphanStations := append(stations, Station{ID: "st-orphan", Slug: "orphan"})
	_, err = Compile(sc, nil, orphanStations, []Service{{
		ID: "svc-1", Active: true, VehicleTypeID: "vt-1",
		Stops: []ServiceStop{{StationID: "st-orphan", Sequence: 1}},
	}}, []VehicleType{vt}, testSegments())
	if err == nil || !strings.Contains(err.Error(), "orphan") {
		t.Fatalf("disconnected stop: want error mentioning orphan, got %v", err)
	}
}

func TestFixture_NewStoreRejectsWouldSurfaceCompileErrors(t *testing.T) {
	_, err := NewStore()
	if err != nil {
		t.Fatalf("valid seed must boot: %v", err)
	}
}
