package transit_test

import (
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func testGraph() *transit.TransitGraph {
	return &transit.TransitGraph{
		Services: []transit.ServiceGraph{
			{
				ServiceID: "svc-1",
				WaitSecs:  60,
				Edges: []transit.Edge{
					{FromSlug: "a", ToSlug: "b", Seconds: 300},
					{FromSlug: "b", ToSlug: "a", Seconds: 300},
				},
			},
		},
		Nodes: []transit.GraphNode{
			{Slug: "a", Lat: 37.1, Lng: -122.1, Names: []string{"A"}},
			{Slug: "b", Lat: 37.2, Lng: -122.2, Names: []string{"B"}},
		},
	}
}

func TestCompiledGraphData_Nodes(t *testing.T) {
	d := transit.CompiledGraphData{Graph: testGraph()}

	nodes, ok := d.Nodes("anything")
	if !ok {
		t.Fatal("Nodes: want ok=true")
	}
	if len(nodes) != 2 {
		t.Fatalf("Nodes: want 2, got %d", len(nodes))
	}
	if nodes[0].Slug != "a" || nodes[0].Lat != 37.1 || nodes[0].Lng != -122.1 {
		t.Errorf("nodes[0] = %+v, want slug a at (37.1, -122.1)", nodes[0])
	}
}

func TestCompiledGraphData_Nodes_nilGraph(t *testing.T) {
	d := transit.CompiledGraphData{}

	if _, ok := d.Nodes("anything"); ok {
		t.Error("Nodes: want ok=false for a nil graph")
	}
}

func TestCompiledGraphData_TravelTimeBetween(t *testing.T) {
	d := transit.CompiledGraphData{Graph: testGraph()}

	secs, wait, serviceID, ok := d.TravelTimeBetween("anything", "a", "b")
	if !ok {
		t.Fatal("TravelTimeBetween: want ok=true")
	}
	if secs != 300 || wait != 60 || serviceID != "svc-1" {
		t.Errorf("TravelTimeBetween = (%d, %d, %q), want (300, 60, svc-1)", secs, wait, serviceID)
	}
}

func TestCompiledGraphData_TravelTimeBetween_sameSlug(t *testing.T) {
	d := transit.CompiledGraphData{Graph: testGraph()}

	secs, wait, _, ok := d.TravelTimeBetween("anything", "a", "a")
	if !ok || secs != 0 || wait != 0 {
		t.Errorf("TravelTimeBetween(a, a) = (%d, %d, ok=%v), want (0, 0, true)", secs, wait, ok)
	}
}

func TestCompiledGraphData_TravelTimeBetween_noPath(t *testing.T) {
	d := transit.CompiledGraphData{Graph: &transit.TransitGraph{}}

	if _, _, _, ok := d.TravelTimeBetween("anything", "a", "b"); ok {
		t.Error("TravelTimeBetween: want ok=false when the graph has no edges")
	}
}
