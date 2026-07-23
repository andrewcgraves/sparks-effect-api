package transit

import (
	"reflect"
	"sort"
	"testing"
)

// Latitude is the clean axis for these fixtures: one degree of latitude is
// 111194.926644 m in the compiler's metric (R * pi/180, the same figure
// project_test.go derives independently), and unlike longitude that does not
// vary with where on Earth the fixture sits. So a separation here is
// hand-checkable: 0.0003 deg is 33.4 m, inside the 50 m merge radius;
// 0.0007 deg is 77.8 m, outside it but inside the near-miss band; 0.0030 deg
// is 333.6 m, outside both.
const (
	latDeltaMerges   = 0.0003 // 33.4 m
	latDeltaNearMiss = 0.0007 // 77.8 m
	latDeltaFar      = 0.0030 // 333.6 m
)

const baseLat, baseLng = 37.7900, -122.3970

// stop builds a CompilableStop at a latitude offset from the fixture origin.
func stop(slug, name string, latDelta float64) CompilableStop {
	return CompilableStop{Slug: slug, Name: name, Lat: baseLat + latDelta, Lng: baseLng}
}

// stopWithOffset is stop plus an OffsetM, simulating a stop that needed
// correcting by that many metres when SnapToRoute put it on its alignment.
func stopWithOffset(slug, name string, latDelta, offsetM float64) CompilableStop {
	s := stop(slug, name, latDelta)
	s.OffsetM = offsetM
	return s
}

// svcOf builds a CompilableService carrying nothing but identity and stops —
// MergeColocatedStops reads no more than that.
func svcOf(id string, stops ...CompilableStop) CompilableService {
	return CompilableService{ID: id, Stops: stops}
}

// keysOf reads back the graph node keys the merge assigned, per service, so a
// test can state its expectation as the keys rather than as cluster internals.
func keysOf(svcs []CompilableService) [][]string {
	out := make([][]string, len(svcs))
	for i, svc := range svcs {
		out[i] = make([]string, len(svc.Stops))
		for j, s := range svc.Stops {
			out[i][j] = s.Slug
		}
	}
	return out
}

// The headline behaviour: two services stopping within the merge radius of
// each other get one graph node key, which is the only way two services ever
// connect (graphDijkstra pools every ServiceGraph's edges into one adjacency
// map keyed by slug).
func TestMergeColocatedStops_colocatedCrossServiceStopsShareOneKey(t *testing.T) {
	svcs := []CompilableService{
		svcOf("svc-a", stop("a--transbay", "Transbay", 0)),
		svcOf("svc-b", stop("b--salesforce", "Salesforce Center", latDeltaMerges)),
	}

	got, _, _ := MergeColocatedStops(svcs)

	keys := keysOf(got)
	if keys[0][0] != keys[1][0] {
		t.Fatalf("keys = %q and %q, want one shared key — without it the two services cannot connect",
			keys[0][0], keys[1][0])
	}
	// "a--transbay" sorts before "b--salesforce", so it is the cluster key.
	if keys[0][0] != "a--transbay" {
		t.Errorf("key = %q, want %q (the lexicographically smallest member slug)", keys[0][0], "a--transbay")
	}
}

// Stops further apart than the merge radius stay distinct, so an unrelated
// pair does not invent a transfer between two places.
func TestMergeColocatedStops_separatedStopsKeepTheirOwnKeys(t *testing.T) {
	svcs := []CompilableService{
		svcOf("svc-a", stop("a--north", "North", 0)),
		svcOf("svc-b", stop("b--south", "South", latDeltaFar)),
	}

	keys := keysOf(mergedOnly(MergeColocatedStops(svcs)))
	if keys[0][0] != "a--north" || keys[1][0] != "b--south" {
		t.Errorf("keys = %q, want each stop to keep its own slug", keys)
	}
}

// mergedOnly drops the report, for tests that assert only on keys.
func mergedOnly(svcs []CompilableService, _ MergeReport, _ []GraphNode) []CompilableService {
	return svcs
}

// The chain case that decides the whole algorithm: A–B within the radius and
// B–C within the radius, but A–C outside it. A single-linkage merge would
// collapse all three; the anchored walk must not. Because membership is tested
// against the anchor and never against an arbitrary member, C measured against
// anchor A is out of reach, so it starts its own cluster. Result: {A,B}, {C}.
//
// The stops are laid out on one axis: A at 0, B at 33.4 m, C at 66.8 m. A–B and
// B–C are each 33.4 m (inside 50), A–C is 66.8 m (outside).
func TestMergeColocatedStops_chainDoesNotPropagateThroughANonAnchor(t *testing.T) {
	svcs := []CompilableService{
		svcOf("svc-a", stop("a", "A", 0)),
		svcOf("svc-b", stop("b", "B", latDeltaMerges)),
		svcOf("svc-c", stop("c", "C", 2*latDeltaMerges)),
	}

	got, report, _ := MergeColocatedStops(svcs)
	keys := keysOf(got)

	// A and B share A's key; C keeps its own.
	if keys[0][0] != "a" || keys[1][0] != "a" {
		t.Errorf("A,B keys = %q,%q, want both %q", keys[0][0], keys[1][0], "a")
	}
	if keys[2][0] != "c" {
		t.Errorf("C key = %q, want %q — the chain must not propagate through B", keys[2][0], "c")
	}

	// The B–C pair that failed to merge is a near miss, not silent.
	if !hasNearMiss(report, "b", "c") {
		t.Errorf("near misses = %+v, want the B–C pair reported", report.NearMisses)
	}
}

// hasNearMiss reports whether the report names the unordered pair of slugs.
func hasNearMiss(r MergeReport, slug1, slug2 string) bool {
	for _, nm := range r.NearMisses {
		if (nm.A.Slug == slug1 && nm.B.Slug == slug2) || (nm.A.Slug == slug2 && nm.B.Slug == slug1) {
			return true
		}
	}
	return false
}

// Determinism, tested by construction rather than asserted: the same stops in
// every permutation of service order must produce byte-identical clusters. The
// walk order is a property of the data, so shuffling the input cannot change
// the output.
func TestMergeColocatedStops_isDeterministicUnderShuffle(t *testing.T) {
	base := []CompilableService{
		svcOf("svc-a", stop("a", "A", 0)),
		svcOf("svc-b", stop("b", "B", latDeltaMerges)),
		svcOf("svc-c", stop("c", "C", 2*latDeltaMerges)),
		svcOf("svc-d", stop("d", "D", latDeltaFar)),
	}

	_, want, _ := MergeColocatedStops(base)

	// Every permutation of the four services.
	perms := permute([]int{0, 1, 2, 3})
	for _, p := range perms {
		shuffled := make([]CompilableService, len(p))
		for i, idx := range p {
			shuffled[i] = base[idx]
		}
		_, got, _ := MergeColocatedStops(shuffled)
		if !reflect.DeepEqual(clusterKeys(got), clusterKeys(want)) {
			t.Fatalf("permutation %v gave clusters %+v, want %+v — merge must not depend on service order",
				p, clusterKeys(got), clusterKeys(want))
		}
	}
}

// clusterKeys reduces a report to the set of (key -> sorted member slugs) so
// two reports can be compared independently of member ordering within a cluster
// (which is itself deterministic, but the determinism under test is of the
// partition, not the slice order).
func clusterKeys(r MergeReport) map[string][]string {
	out := make(map[string][]string, len(r.Clusters))
	for _, c := range r.Clusters {
		slugs := make([]string, len(c.Members))
		for i, m := range c.Members {
			slugs[i] = m.Slug
		}
		sort.Strings(slugs)
		out[c.Key] = slugs
	}
	return out
}

// permute returns all permutations of the given indices.
func permute(xs []int) [][]int {
	if len(xs) <= 1 {
		return [][]int{append([]int(nil), xs...)}
	}
	var out [][]int
	for i := range xs {
		rest := append(append([]int(nil), xs[:i]...), xs[i+1:]...)
		for _, p := range permute(rest) {
			out = append(out, append([]int{xs[i]}, p...))
		}
	}
	return out
}

// Two stops of the *same* service never merge, however close. A service that
// stops twice within the radius is a loop or a switchback, not an interchange,
// and merging it would collapse a real span onto one node.
func TestMergeColocatedStops_neverMergesWithinOneService(t *testing.T) {
	svcs := []CompilableService{
		svcOf("svc-a",
			stop("a--first", "First", 0),
			stop("a--second", "Second", latDeltaMerges),
		),
	}

	got, report, _ := MergeColocatedStops(svcs)
	keys := keysOf(got)
	if keys[0][0] == keys[0][1] {
		t.Errorf("keys = %q, want two distinct keys — a service's own stops must not merge", keys[0])
	}
	if len(report.Clusters) != 0 {
		t.Errorf("clusters = %+v, want none for a single service", report.Clusters)
	}
	// And they are not reported as a near miss either — same-service pairs were
	// never merge candidates.
	if len(report.NearMisses) != 0 {
		t.Errorf("near misses = %+v, want none for same-service stops", report.NearMisses)
	}
}

// A single-service compile produces keys identical to that service's own stop
// slugs: every cluster is a singleton, so the anchor (and hence the key) is the
// stop's own slug. This is what lets a single-service compile go through the
// same path as a scenario with no special case.
func TestMergeColocatedStops_singleServiceKeysAreUnchanged(t *testing.T) {
	svcs := []CompilableService{
		svcOf("svc-a",
			stop("a--north", "North", 0),
			stop("a--mid", "Mid", latDeltaFar),
			stop("a--south", "South", 2*latDeltaFar),
		),
	}

	got, report, _ := MergeColocatedStops(svcs)
	want := []string{"a--north", "a--mid", "a--south"}
	if !reflect.DeepEqual(keysOf(got)[0], want) {
		t.Errorf("keys = %q, want %q unchanged", keysOf(got)[0], want)
	}
	if len(report.Clusters) != 0 || len(report.NearMisses) != 0 {
		t.Errorf("report = %+v, want empty for a lone service", report)
	}
}

// A realised cluster carries every member's name, deduplicated, so a caller can
// render all of them. The key's name comes first.
func TestMergeColocatedStops_clusterCarriesAllMemberNames(t *testing.T) {
	svcs := []CompilableService{
		svcOf("svc-a", stop("a--transbay", "Transbay", 0)),
		svcOf("svc-b", stop("b--sales", "Salesforce Center", latDeltaMerges)),
		svcOf("svc-c", stop("c--transbay", "Transbay", latDeltaMerges)),
	}

	_, report, _ := MergeColocatedStops(svcs)
	if len(report.Clusters) != 1 {
		t.Fatalf("clusters = %+v, want exactly one", report.Clusters)
	}
	// "Transbay" appears twice among members but is one name; the cluster names
	// it once, first (the key's name), then the distinct "Salesforce Center".
	want := []string{"Transbay", "Salesforce Center"}
	if !reflect.DeepEqual(report.Clusters[0].Names, want) {
		t.Errorf("names = %q, want %q", report.Clusters[0].Names, want)
	}
}

// A near miss names two same-named stops on crossing alignments that landed
// just outside the merge radius — the motivating false-negative case. The pair
// is 77.8 m apart: outside 50 m, inside the 250 m near-miss band.
func TestMergeColocatedStops_reportsSameNamedNearMiss(t *testing.T) {
	svcs := []CompilableService{
		svcOf("svc-a", stop("a--transbay", "Transbay", 0)),
		svcOf("svc-b", stop("b--transbay", "Transbay", latDeltaNearMiss)),
	}

	got, report, _ := MergeColocatedStops(svcs)
	// Did not merge.
	if keysOf(got)[0][0] == keysOf(got)[1][0] {
		t.Fatal("stops merged, want them left separate at 77.8 m")
	}
	if !hasNearMiss(report, "a--transbay", "b--transbay") {
		t.Fatalf("near misses = %+v, want the Transbay pair reported", report.NearMisses)
	}
	// The reported distance is the real separation, roughly 77.8 m.
	nm := report.NearMisses[0]
	if nm.DistanceM < 76 || nm.DistanceM > 80 {
		t.Errorf("distance = %v, want ~77.8 m", nm.DistanceM)
	}
}

// The acceptance headline as a journey: board service A, ride to a stop it
// shares with service B, and continue on B to a place only B reaches. Without
// the merge the two services share no key and graphDijkstra finds nothing; with
// it they meet at one node and the journey exists. This is the only mechanism
// by which a curated set of services becomes a network.
//
// Service A runs west->east and stops at "a--west" and "a--cross". Service B
// runs south->north from "b--cross" (co-located with "a--cross") to "b--north".
// A rider starting at a--west can only reach b--north if the two cross-slugs
// merged.
func TestCompileServices_journeyCrossesAMergedNode(t *testing.T) {
	const crossLng, crossLat = 0.01, 0.0
	svcA := CompilableService{
		ID:      "svc-a",
		Route:   Route{Geometry: GeoLineString{Coordinates: [][]float64{{0, 0}, {crossLng, crossLat}}}},
		Vehicle: physicsKinematics(),
		Stops: []CompilableStop{
			{Slug: "a--west", Name: "West", Lng: 0, Lat: 0},
			{Slug: "a--cross", Name: "Cross", Lng: crossLng, Lat: crossLat},
		},
	}
	svcB := CompilableService{
		ID:      "svc-b",
		Route:   Route{Geometry: GeoLineString{Coordinates: [][]float64{{crossLng, crossLat}, {crossLng, 0.01}}}},
		Vehicle: physicsKinematics(),
		Stops: []CompilableStop{
			// ~1 m north of a--cross: inside the merge radius, different service.
			{Slug: "b--cross", Name: "Cross", Lng: crossLng, Lat: crossLat + 0.00001},
			{Slug: "b--north", Name: "North", Lng: crossLng, Lat: 0.01},
		},
	}

	graph, err := CompileServices([]CompilableService{svcA, svcB})
	if err != nil {
		t.Fatalf("CompileServices() error = %v, want nil", err)
	}

	// The cross stops merged onto a--cross (it sorts before b--cross).
	secs, _, _, ok := graphDijkstra(&graph, "a--west", "b--north")
	if !ok {
		t.Fatal("no journey a--west -> b--north: the crossing node did not merge")
	}
	if secs <= 0 {
		t.Errorf("journey secs = %d, want > 0", secs)
	}

	// And the merge is reported on the graph, naming the node the journey
	// passed through.
	if len(graph.Merge.Clusters) != 1 || graph.Merge.Clusters[0].Key != "a--cross" {
		t.Errorf("clusters = %+v, want one keyed a--cross", graph.Merge.Clusters)
	}
}

// physicsKinematics mirrors physicsTestVehicle's clean kinematics as a
// CompilableService.Vehicle, for tests that build Compilables directly.
func physicsKinematics() Kinematics {
	return Kinematics{MaxSpeedKMH: 36, AccelerationMS2: 1, DecelerationMS2: 1}
}

// SPA-113: the merge radius widens by both stops' OffsetM, capped at
// MaxMergeRadiusM. These pin effectiveMergeRadius directly before the tests
// below exercise it through MergeColocatedStops.
func TestEffectiveMergeRadius_zeroOffsetsIsTheBaseRadius(t *testing.T) {
	if got := effectiveMergeRadius(0, 0); got != MergeRadiusM {
		t.Errorf("effectiveMergeRadius(0, 0) = %v, want %v", got, MergeRadiusM)
	}
}

func TestEffectiveMergeRadius_widensByBothStopsOffset(t *testing.T) {
	got := effectiveMergeRadius(100, 150)
	want := MergeRadiusM + 100 + 150
	if got != want {
		t.Errorf("effectiveMergeRadius(100, 150) = %v, want %v", got, want)
	}
}

func TestEffectiveMergeRadius_capsAtMaxMergeRadiusM(t *testing.T) {
	// Both stops near their own 500 m off-route limit: 50+500+500 = 1050,
	// which must not exceed the 500 m ceiling.
	if got := effectiveMergeRadius(500, 500); got != MaxMergeRadiusM {
		t.Errorf("effectiveMergeRadius(500, 500) = %v, want the %v ceiling", got, MaxMergeRadiusM)
	}
}

// A pair that a flat 50 m radius would miss — 333.6 m apart, also outside the
// 250 m near-miss band — merges once each stop's snapping offset is counted,
// because 50+200+150 = 400 covers the separation.
func TestMergeColocatedStops_offsetsWidenRadiusEnoughToMerge(t *testing.T) {
	svcs := []CompilableService{
		svcOf("svc-a", stopWithOffset("a--transbay", "Transbay", 0, 200)),
		svcOf("svc-b", stopWithOffset("b--salesforce", "Salesforce Center", latDeltaFar, 150)),
	}

	got, report, _ := MergeColocatedStops(svcs)
	keys := keysOf(got)
	if keys[0][0] != keys[1][0] {
		t.Fatalf("keys = %q and %q, want one shared key — the widened radius should have covered 333.6 m",
			keys[0][0], keys[1][0])
	}
	if len(report.Clusters) != 1 {
		t.Errorf("clusters = %+v, want exactly one realised merge", report.Clusters)
	}
}
