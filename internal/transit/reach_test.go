package transit

import (
	"math"
	"testing"
)

// stationsAt builds a graph whose nodes sit at the given positions. Only the
// node set matters here — the reach check never looks at an edge.
func stationsAt(positions ...GraphNode) *TransitGraph {
	return &TransitGraph{Nodes: positions}
}

// northOf returns a point km kilometres due north of the origin below. Going
// north rather than east keeps the conversion exact at any latitude: a degree
// of latitude is the same distance everywhere, while a degree of longitude is
// not.
const (
	reachLat    = 37.7
	reachLng    = -122.4
	kmPerDegLat = 111.194926644559
)

func northOf(km float64) GraphNode {
	return GraphNode{Slug: "far", Lat: reachLat + km/kmPerDegLat, Lng: reachLng}
}

// The boundary, per mode, at the budgets the UI offers. A station just inside
// the radius is a request worth enqueueing; one just outside is provably
// unreachable and is the whole point of the check.
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

// The comparison is inclusive: a station exactly at the limit is reachable in
// exactly the budget, so refusing it would refuse a request that works.
func TestCheckOriginReach_aStationExactlyAtTheLimitIsInRange(t *testing.T) {
	got, ok := CheckOriginReach(stationsAt(northOf(2.5)), reachLat, reachLng, TravelModeWalk, 30)
	if !ok {
		t.Fatal("a graph with a node should be checkable")
	}
	if !got.InRange {
		t.Errorf("a station at exactly %.3f km is not in range of %.3f km", got.NearestKm, got.MaxReachKm)
	}
}

// One station in range is enough, however far away the rest are — and it is the
// nearest that gets reported, not whichever the graph happened to list first.
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

// Out of range still names the nearest station and how far it was, because
// that is what turns the refusal into something a person can act on.
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

// A graph the check cannot see any stations in tells us nothing about the
// origin, so it must not be read as "out of range". Each of these is a
// different request problem, and none of them is this one.
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

// Every mode TravelMode.Valid accepts has a speed here. Without this, adding a
// fourth mode would silently make the check unanswerable for it — permissive,
// so nothing would break loudly, and the guard would just stop applying.
func TestCheckOriginReach_coversEveryValidMode(t *testing.T) {
	for _, mode := range []TravelMode{TravelModeWalk, TravelModeBike, TravelModeDrive} {
		if !mode.Valid() {
			t.Fatalf("%q is not a valid mode; fix the test, not the code", mode)
		}
		if _, ok := CheckOriginReach(stationsAt(northOf(1)), reachLat, reachLng, mode, 30); !ok {
			t.Errorf("mode %q has no speed, so no origin can ever be refused in it", mode)
		}
	}
}
