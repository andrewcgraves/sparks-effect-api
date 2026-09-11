package transit

import (
	"math"
	"testing"
)

func stationsAt(positions ...GraphNode) *TransitGraph {
	return &TransitGraph{Nodes: positions}
}

const (
	reachLat    = 37.7
	reachLng    = -122.4
	kmPerDegLat = 111.194926644559
)

func northOf(km float64) GraphNode {
	return GraphNode{Slug: "far", Lat: reachLat + km/kmPerDegLat, Lng: reachLng}
}

func TestCheckOriginReach_theBoundaryPerMode(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode       TravelMode
		budgetMins int
		reachKm    float64
	}{
		{"walk 30", TravelModeWalk, 30, 2.5},
		{"walk 240", TravelModeWalk, 240, 20},
		{"bike 60", TravelModeBike, 60, 15},
		{"drive 120", TravelModeDrive, 120, 160},
		{"transit 60", TravelModeTransit, 60, 40},
		{"transit 90", TravelModeTransit, 90, 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inside := stationsAt(northOf(tc.reachKm * 0.99))
			got, ok := CheckOriginReach(inside, reachLat, reachLng, tc.mode, tc.budgetMins)
			if !ok {
				t.Fatal("a graph with a node should be checkable")
			}
			if !got.InRange {
				t.Errorf("a station at %.3f km is not in range of %.3f km", got.NearestKm, got.MaxReachKm)
			}

			outside := stationsAt(northOf(tc.reachKm * 1.01))
			got, ok = CheckOriginReach(outside, reachLat, reachLng, tc.mode, tc.budgetMins)
			if !ok {
				t.Fatal("a graph with a node should be checkable")
			}
			if got.InRange {
				t.Errorf("a station at %.3f km is in range of %.3f km", got.NearestKm, got.MaxReachKm)
			}
			if math.Abs(got.MaxReachKm-tc.reachKm) > 1e-9 {
				t.Errorf("MaxReachKm = %v, want %v", got.MaxReachKm, tc.reachKm)
			}
		})
	}
}

func TestCheckOriginReach_aStationExactlyAtTheLimitIsInRange(t *testing.T) {
	got, ok := CheckOriginReach(stationsAt(northOf(2.5)), reachLat, reachLng, TravelModeWalk, 30)
	if !ok {
		t.Fatal("a graph with a node should be checkable")
	}
	if !got.InRange {
		t.Errorf("a station at exactly %.3f km is not in range of %.3f km", got.NearestKm, got.MaxReachKm)
	}
}

func TestCheckOriginReach_reportsTheNearestStation(t *testing.T) {
	graph := stationsAt(
		GraphNode{Slug: "far", Lat: reachLat + 1, Lng: reachLng},
		GraphNode{Slug: "near", Lat: reachLat + 0.01, Lng: reachLng},
		GraphNode{Slug: "middling", Lat: reachLat + 0.5, Lng: reachLng},
	)

	got, ok := CheckOriginReach(graph, reachLat, reachLng, TravelModeWalk, 30)
	if !ok {
		t.Fatal("a graph with nodes should be checkable")
	}
	if got.NearestSlug != "near" {
		t.Errorf("NearestSlug = %q, want near", got.NearestSlug)
	}
	if !got.InRange {
		t.Error("one station within reach should put the origin in range")
	}
}

func TestCheckOriginReach_outOfRangeStillDescribesTheNearest(t *testing.T) {
	graph := stationsAt(
		GraphNode{Slug: "further", Lat: reachLat + 2, Lng: reachLng},
		GraphNode{Slug: "closest", Lat: reachLat + 1, Lng: reachLng},
	)

	got, ok := CheckOriginReach(graph, reachLat, reachLng, TravelModeWalk, 30)
	if !ok {
		t.Fatal("a graph with nodes should be checkable")
	}
	if got.InRange {
		t.Fatal("stations ~111 km away should not be in range of a 30-minute walk")
	}
	if got.NearestSlug != "closest" {
		t.Errorf("NearestSlug = %q, want closest", got.NearestSlug)
	}
	if math.Abs(got.NearestKm-kmPerDegLat) > 1 {
		t.Errorf("NearestKm = %v, want ~%v", got.NearestKm, kmPerDegLat)
	}
}

func TestCheckOriginReach_unanswerableWhenThereIsNothingToMeasureAgainst(t *testing.T) {
	for _, tc := range []struct {
		name  string
		graph *TransitGraph
		mode  TravelMode
	}{
		{"nil graph", nil, TravelModeWalk},
		{"graph with no nodes", &TransitGraph{}, TravelModeWalk},
		{"unknown mode", stationsAt(northOf(500)), TravelMode("teleport")},
		{"empty mode", stationsAt(northOf(500)), TravelMode("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := CheckOriginReach(tc.graph, reachLat, reachLng, tc.mode, 30); ok {
				t.Error("the check reported an answer it had no basis for")
			}
		})
	}
}

func TestCheckOriginReach_coversEveryValidMode(t *testing.T) {
	for _, mode := range TravelModes() {
		if !mode.Valid() {
			t.Fatalf("%q is not a valid mode; fix the test, not the code", mode)
		}
		if _, ok := CheckOriginReach(stationsAt(northOf(1)), reachLat, reachLng, mode, 30); !ok {
			t.Errorf("mode %q has no speed, so no origin can ever be refused in it", mode)
		}
	}
}
