package transit

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The node set is where a compiled graph's geometry lives, so a merged cluster
// must surface as one node carrying the key member's position and every
// member's name — the two facts SPA-83's chainer and the UI read off it.
func TestMergeColocatedStops_nodeCarriesKeyMemberCoordAndAllNames(t *testing.T) {
	svcs := []CompilableService{
		svcOf("svc-a", stop("a--transbay", "Transbay", 0)),
		svcOf("svc-b", stop("b--salesforce", "Salesforce Center", latDeltaMerges)),
	}

	_, _, nodes := MergeColocatedStops(svcs, nil)

	node, ok := nodeBySlug(nodes)["a--transbay"]
	if !ok {
		t.Fatalf("no node for merged key a--transbay; nodes = %+v", nodes)
	}
	// The position is the key member's (a--transbay, at latDelta 0), not the
	// centroid of the two — a centroid would sit at latDeltaMerges/2, on no
	// route line.
	if node.Lat != baseLat || node.Lng != baseLng {
		t.Errorf("node position = (%v, %v), want key member's (%v, %v)", node.Lat, node.Lng, baseLat, baseLng)
	}
	want := []string{"Transbay", "Salesforce Center"}
	if !reflect.DeepEqual(node.Names, want) {
		t.Errorf("node.Names = %v, want %v (key member first)", node.Names, want)
	}
}

// A stop that merged with nothing is still a graph node — its own service's
// edges name it — even though the merge report drops it as a non-interchange.
func TestMergeColocatedStops_nodesIncludeSingletons(t *testing.T) {
	svcs := []CompilableService{
		svcOf("svc-a", stop("a--transbay", "Transbay", 0)),
		svcOf("svc-b", stop("b--salesforce", "Salesforce Center", latDeltaMerges)),
		svcOf("svc-c", stop("c--daly", "Daly City", latDeltaFar)),
	}

	_, report, nodes := MergeColocatedStops(svcs, nil)

	if len(report.Clusters) != 1 {
		t.Fatalf("len(report.Clusters) = %d, want 1 (only the merge)", len(report.Clusters))
	}
	bySlug := nodeBySlug(nodes)
	if len(bySlug) != 2 {
		t.Fatalf("distinct node keys = %d, want 2 (merged pair + singleton)", len(bySlug))
	}
	daly, ok := bySlug["c--daly"]
	if !ok {
		t.Fatalf("singleton c--daly missing from nodes; nodes = %+v", nodes)
	}
	if want := []string{"Daly City"}; !reflect.DeepEqual(daly.Names, want) {
		t.Errorf("singleton Names = %v, want %v", daly.Names, want)
	}
}

// crossingScenario is two services on crossing alignments that share one
// interchange: rt-h runs east along lat 0, rt-v runs north along lng 0.5, and
// each has a stop at their crossing — cross-a and cross-b, two stations metres
// apart that the merge folds onto one node. It is the smallest scenario whose
// graph has an edge key contributed by a *merged* cluster, which is the closure
// case this ticket exists to guarantee.
func crossingScenario() ([]Route, []Station, []Service, []VehicleType) {
	routes := []Route{
		{ID: "rt-h", Slug: "rt-h", Geometry: GeoLineString{Type: "LineString", Coordinates: [][]float64{{0, 0}, {1, 0}}}},
		{ID: "rt-v", Slug: "rt-v", Geometry: GeoLineString{Type: "LineString", Coordinates: [][]float64{{0.5, -1}, {0.5, 1}}}},
	}
	stations := []Station{
		{ID: "st-west", Slug: "west", Name: "West", Location: GeoPoint{Coordinates: []float64{0, 0}}, PlatformHeight: "high"},
		{ID: "st-cross-a", Slug: "cross-a", Name: "Crossing A", Location: GeoPoint{Coordinates: []float64{0.5, 0}}, PlatformHeight: "high"},
		// 0.0003 deg latitude north of cross-a: 33.4 m, inside the 50 m merge
		// radius, so the two merge — and far enough that a centroid would be a
		// visibly different point from the key member's coordinate.
		{ID: "st-cross-b", Slug: "cross-b", Name: "Crossing B", Location: GeoPoint{Coordinates: []float64{0.5, 0.0003}}, PlatformHeight: "high"},
		{ID: "st-north", Slug: "north", Name: "North", Location: GeoPoint{Coordinates: []float64{0.5, 0.5}}, PlatformHeight: "high"},
	}
	services := []Service{
		{ID: "svc-a", RouteID: "rt-h", VehicleTypeID: "vt-physics", Active: true, Stops: []ServiceStop{
			{StationID: "st-west", Sequence: 1}, {StationID: "st-cross-a", Sequence: 2},
		}},
		{ID: "svc-b", RouteID: "rt-v", VehicleTypeID: "vt-physics", Active: true, Stops: []ServiceStop{
			{StationID: "st-cross-b", Sequence: 1}, {StationID: "st-north", Sequence: 2},
		}},
	}
	return routes, stations, services, []VehicleType{physicsTestVehicle()}
}

// The closure this ticket exists to guarantee: every slug an edge names has
// exactly one node, and every node is named by some edge. A dangling edge key
// is precisely the failure SPA-83's chainer would hit — a node key with no
// location — so it is asserted directly on a compiled graph.
func TestCompileScenario_nodesCloseOverEveryEdgeKey(t *testing.T) {
	graph, err := CompileScenario(crossingScenario())
	if err != nil {
		t.Fatalf("CompileScenario() error = %v, want nil", err)
	}

	bySlug := nodeBySlug(graph.Nodes)
	if len(bySlug) != len(graph.Nodes) {
		t.Fatalf("duplicate node slug: %d nodes but %d distinct slugs", len(graph.Nodes), len(bySlug))
	}

	edgeKeys := make(map[string]bool)
	for _, sg := range graph.Services {
		for _, e := range sg.Edges {
			edgeKeys[e.FromSlug] = true
			edgeKeys[e.ToSlug] = true
		}
	}
	if len(edgeKeys) == 0 {
		t.Fatal("graph has no edges; the fixture is not exercising the compiler")
	}

	for key := range edgeKeys {
		if _, ok := bySlug[key]; !ok {
			t.Errorf("edge key %q has no matching node — dangling key", key)
		}
	}
	for slug := range bySlug {
		if !edgeKeys[slug] {
			t.Errorf("node %q is named by no edge — orphan node", slug)
		}
	}
}

// The merged interchange node carries the cluster-key member's coordinate, not
// a centroid, all the way through the physics compile.
func TestCompileScenario_mergedNodeUsesKeyMemberCoordinate(t *testing.T) {
	graph, err := CompileScenario(crossingScenario())
	if err != nil {
		t.Fatalf("CompileScenario() error = %v, want nil", err)
	}

	// cross-a sorts before cross-b, so it is the anchor and the key.
	node, ok := nodeBySlug(graph.Nodes)["cross-a"]
	if !ok {
		t.Fatalf("no node for merged key cross-a; nodes = %+v", graph.Nodes)
	}
	if node.Lat != 0 || node.Lng != 0.5 {
		t.Errorf("merged node position = (%v, %v), want cross-a's (0, 0.5), not a centroid", node.Lat, node.Lng)
	}
	if want := []string{"Crossing A", "Crossing B"}; !reflect.DeepEqual(node.Names, want) {
		t.Errorf("merged node Names = %v, want %v", node.Names, want)
	}
}

// AC3 end-to-end on the user-authored path: a user service's stops carry the
// coordinates SPA-108 snapped and persisted on write (ServiceStopPoint.Lat/Lng),
// and the compiled node must be that exact persisted position — not re-snapped
// and not re-derived. This is the path a real user scenario takes; the seeded
// adapter reads a station location instead, so only compiling a UserService
// proves the persisted snapped coordinate rides all the way through to the node.
func TestCompileServices_userServiceNodeCarriesPersistedSnappedCoord(t *testing.T) {
	svc := UserService{
		ID:      "us-1",
		Slug:    "my-line",
		RouteID: "rt-1",
		Vehicle: VehicleParams{MaxSpeedKMH: 200, AccelerationMS2: 1, DecelerationMS2: 1, DwellS: 30},
		Stops: []ServiceStopPoint{
			// Lat/Lng are the persisted snapped positions; ChainageM/OffsetM
			// record the write that produced them (SPA-108).
			{Name: "North End", Lat: 0, Lng: 0.25, Seq: 0, ChainageM: 27798, OffsetM: 12},
			{Name: "South End", Lat: 0, Lng: 0.75, Seq: 1, ChainageM: 83395, OffsetM: 4},
		},
		FrequencyWindows: []FrequencyWindow{{StartTime: "06:00", EndTime: "09:00", HeadwayS: 300}},
	}

	cs := mustCompilableFromUserService(t, adapterRoute(), svc)
	graph, err := CompileServices([]CompilableService{cs}, nil)
	if err != nil {
		t.Fatalf("CompileServices() error = %v, want nil", err)
	}

	bySlug := nodeBySlug(graph.Nodes)
	for _, want := range []struct {
		slug     string
		lat, lng float64
	}{
		{"my-line--north-end", 0, 0.25},
		{"my-line--south-end", 0, 0.75},
	} {
		node, ok := bySlug[want.slug]
		if !ok {
			t.Fatalf("no node for %q; nodes = %+v", want.slug, graph.Nodes)
		}
		if node.Lat != want.lat || node.Lng != want.lng {
			t.Errorf("node %q position = (%v, %v), want persisted snapped (%v, %v)",
				want.slug, node.Lat, node.Lng, want.lat, want.lng)
		}
	}
}

// The field is additive and optional on decode: a jobs.result row written
// before this change — no "nodes" key — still unmarshals, with Nodes nil. No
// backfill; a legacy row is simply a graph without geometry.
func TestTransitGraph_decodesLegacyResultWithoutNodes(t *testing.T) {
	legacy := `{"services":[{"service_id":"svc-1","edges":[{"from_slug":"a","to_slug":"b","seconds":60}],"wait_secs":0}]}`

	var g TransitGraph
	if err := json.Unmarshal([]byte(legacy), &g); err != nil {
		t.Fatalf("unmarshal legacy result: %v", err)
	}
	if g.Nodes != nil {
		t.Errorf("Nodes = %+v, want nil for a pre-nodes result", g.Nodes)
	}
	if len(g.Services) != 1 || g.Services[0].ServiceID != "svc-1" {
		t.Errorf("Services = %+v, want the one decoded service", g.Services)
	}
}

// omitempty holds up the other direction too: a graph with no nodes marshals
// without a "nodes" key, so the hand-authored path's result is byte-unchanged.
func TestTransitGraph_omitsNodesWhenEmpty(t *testing.T) {
	b, err := json.Marshal(TransitGraph{Services: []ServiceGraph{{ServiceID: "svc-1"}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(b); strings.Contains(got, `"nodes"`) {
		t.Errorf("marshalled graph = %s, want no \"nodes\" key", got)
	}
}

// The hand-authored Compile is left carrying no nodes: its seeded isochrone
// still sources positions from GetStationsByScenario, and making Nodes
// authoritative there is a separate, migration-shaped decision.
func TestCompile_handAuthoredGraphHasNoNodes(t *testing.T) {
	sc := Scenario{ID: "sc-1", Slug: "test"}
	services := []Service{{
		ID: "svc-local", Active: true, Name: "Local", VehicleTypeID: "vt-1",
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
			{StationID: "st-c", Sequence: 3},
		},
	}}

	g, err := Compile(sc, nil, testStations(), services, []VehicleType{testVehicle()}, testSegments())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if g.Nodes != nil {
		t.Errorf("Nodes = %+v, want nil for a hand-authored compile", g.Nodes)
	}
}

func nodeBySlug(nodes []GraphNode) map[string]GraphNode {
	out := make(map[string]GraphNode, len(nodes))
	for _, n := range nodes {
		out[n.Slug] = n
	}
	return out
}
