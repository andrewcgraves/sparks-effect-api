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

	got, _, _ := MergeColocatedStops(svcs, nil)

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

	keys := keysOf(mergedOnly(MergeColocatedStops(svcs, nil)))
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

	got, report, _ := MergeColocatedStops(svcs, nil)
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

	_, want, _ := MergeColocatedStops(base, nil)

	// Every permutation of the four services.
	perms := permute([]int{0, 1, 2, 3})
	for _, p := range perms {
		shuffled := make([]CompilableService, len(p))
		for i, idx := range p {
			shuffled[i] = base[idx]
		}
		_, got, _ := MergeColocatedStops(shuffled, nil)
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

	got, report, _ := MergeColocatedStops(svcs, nil)
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

	got, report, _ := MergeColocatedStops(svcs, nil)
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

	_, report, _ := MergeColocatedStops(svcs, nil)
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

	got, report, _ := MergeColocatedStops(svcs, nil)
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

	graph, err := CompileServices([]CompilableService{svcA, svcB}, nil)
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

// SPA-120: a declared pair merges two stops regardless of how far apart they
// actually are. ~2.2 km is far past even MaxMergeRadiusM (500 m) — nothing
// short of a declared pair could ever have merged or even near-missed this on
// its own.
func TestMergeColocatedStops_declaredPairMergesRegardlessOfDistance(t *testing.T) {
	const latDeltaKm = 0.02 // ~2.22 km
	svcs := []CompilableService{
		svcOf("svc-a", stop("a--downtown", "Downtown", 0)),
		svcOf("svc-b", stop("b--downtown", "Downtown", latDeltaKm)),
	}
	pairs := []InterchangePair{
		{A: StopIdentity{ServiceID: "svc-a", Slug: "a--downtown"}, B: StopIdentity{ServiceID: "svc-b", Slug: "b--downtown"}},
	}

	got, report, nodes := MergeColocatedStops(svcs, pairs)
	keys := keysOf(got)
	if keys[0][0] != keys[1][0] {
		t.Fatalf("keys = %q and %q, want one shared key — a declared pair must merge regardless of distance", keys[0][0], keys[1][0])
	}
	if len(report.Clusters) != 1 {
		t.Fatalf("clusters = %+v, want exactly one realised merge", report.Clusters)
	}
	if len(nodes) != 1 {
		t.Errorf("nodes = %+v, want one merged node", nodes)
	}
	if len(report.NearMisses) != 0 {
		t.Errorf("near misses = %+v, want none — the pair merged, it did not almost merge", report.NearMisses)
	}
}

// Declaring a pair already within the proximity merge radius is a no-op, not
// an error: the two stops already share a cluster, so there is nothing for
// the fold to do.
func TestMergeColocatedStops_declaredPairAlreadyInRadiusIsNoOp(t *testing.T) {
	svcs := []CompilableService{
		svcOf("svc-a", stop("a--transbay", "Transbay", 0)),
		svcOf("svc-b", stop("b--salesforce", "Salesforce Center", latDeltaMerges)),
	}
	pairs := []InterchangePair{
		{A: StopIdentity{ServiceID: "svc-a", Slug: "a--transbay"}, B: StopIdentity{ServiceID: "svc-b", Slug: "b--salesforce"}},
	}

	got, report, _ := MergeColocatedStops(svcs, pairs)
	keys := keysOf(got)
	if keys[0][0] != "a--transbay" || keys[1][0] != "a--transbay" {
		t.Fatalf("keys = %q, want both merged onto %q exactly as proximity alone gives", keys, "a--transbay")
	}
	if len(report.Clusters) != 1 {
		t.Errorf("clusters = %+v, want exactly one — an already-merged declared pair must not duplicate it", report.Clusters)
	}
}

// A declared pair folds whole clusters together, not just the two named
// stops: if the declared stop already sits in a multi-member proximity
// cluster, every one of that cluster's members comes along. svc-a and svc-c
// are within the proximity radius of each other (already one cluster before
// any pair is declared); declaring svc-a--svc-x the same place pulls svc-c in
// too, even though C-X was never declared and is kilometres outside any
// merge or near-miss radius. This is deliberate, not an accident of the
// fold's implementation order: a declared pair asserts a place, not a
// stop-to-stop link, and svc-c already shares svc-a's place by proximity.
func TestMergeColocatedStops_declaredPairFoldsInProximityClusterMatesToo(t *testing.T) {
	svcs := []CompilableService{
		svcOf("svc-a", stop("a", "A", 0)),
		svcOf("svc-c", stop("c", "C", latDeltaMerges)), // within radius of A: one proximity cluster
		svcOf("svc-x", stop("x", "X", 10*latDeltaFar)), // kilometres from A and C alike
	}
	pairs := []InterchangePair{
		{A: StopIdentity{ServiceID: "svc-a", Slug: "a"}, B: StopIdentity{ServiceID: "svc-x", Slug: "x"}},
	}

	got, report, nodes := MergeColocatedStops(svcs, pairs)
	keys := keysOf(got)
	if keys[0][0] != keys[1][0] || keys[0][0] != keys[2][0] {
		t.Fatalf("keys = %q, %q, %q, want all three sharing one key — C rides along with A's declared merge", keys[0][0], keys[1][0], keys[2][0])
	}
	if len(report.Clusters) != 1 || len(report.Clusters[0].Members) != 3 {
		t.Fatalf("clusters = %+v, want one cluster of three members", report.Clusters)
	}
	if len(nodes) != 1 {
		t.Errorf("nodes = %+v, want one merged node", nodes)
	}
}

// Declaring interchange for one pair must not change the outcome for any
// other pair. This reproduces the chain case from
// TestMergeColocatedStops_chainDoesNotPropagateThroughANonAnchor verbatim —
// A merges onto B, C stays separate, the B-C near miss is reported — with an
// unrelated declared pair between two wholly different, kilometres-distant
// stops folded in alongside it. The chain's outcome must be untouched.
func TestMergeColocatedStops_declaringOnePairDoesNotChangeAnotherPairsOutcome(t *testing.T) {
	svcs := []CompilableService{
		svcOf("svc-a", stop("a", "A", 0)),
		svcOf("svc-b", stop("b", "B", latDeltaMerges)),
		svcOf("svc-c", stop("c", "C", 2*latDeltaMerges)),
		svcOf("svc-x", stop("x--far1", "Far1", 10*latDeltaFar)),
		svcOf("svc-y", stop("y--far2", "Far2", 20*latDeltaFar)),
	}
	pairs := []InterchangePair{
		{A: StopIdentity{ServiceID: "svc-x", Slug: "x--far1"}, B: StopIdentity{ServiceID: "svc-y", Slug: "y--far2"}},
	}

	got, report, _ := MergeColocatedStops(svcs, pairs)
	keys := keysOf(got)

	if keys[0][0] != "a" || keys[1][0] != "a" {
		t.Errorf("A,B keys = %q,%q, want both %q — unchanged by the unrelated declared pair", keys[0][0], keys[1][0], "a")
	}
	if keys[2][0] != "c" {
		t.Errorf("C key = %q, want %q — unchanged", keys[2][0], "c")
	}
	if !hasNearMiss(report, "b", "c") {
		t.Errorf("near misses = %+v, want the B-C pair still reported", report.NearMisses)
	}

	// And the declared pair still did its own job.
	if keys[3][0] != keys[4][0] {
		t.Errorf("X,Y keys = %q,%q, want merged by the declared pair", keys[3][0], keys[4][0])
	}
}

// A declared pair naming two stops on the same service is rejected outright,
// at the CompileServices seam: letting it through would surface later as a
// duplicate-slug compile failure with the wrong stated cause.
func TestCompileServices_rejectsInterchangePairOnSameService(t *testing.T) {
	svcs := []CompilableService{
		svcOf("svc-a", stop("a--north", "North", 0), stop("a--south", "South", latDeltaFar)),
	}
	pairs := []InterchangePair{
		{A: StopIdentity{ServiceID: "svc-a", Slug: "a--north"}, B: StopIdentity{ServiceID: "svc-a", Slug: "a--south"}},
	}

	if _, err := CompileServices(svcs, pairs); err == nil {
		t.Error("CompileServices() error = nil, want an error for a same-service interchange pair")
	}
}

// A declared pair naming a stop slug absent from the named service is a
// caller error, not a silently-ignored no-op.
func TestCompileServices_rejectsInterchangePairWithUnknownStop(t *testing.T) {
	svcs := []CompilableService{
		svcOf("svc-a", stop("a--north", "North", 0)),
		svcOf("svc-b", stop("b--south", "South", latDeltaFar)),
	}
	pairs := []InterchangePair{
		{A: StopIdentity{ServiceID: "svc-a", Slug: "a--north"}, B: StopIdentity{ServiceID: "svc-b", Slug: "b--nonexistent"}},
	}

	if _, err := CompileServices(svcs, pairs); err == nil {
		t.Error("CompileServices() error = nil, want an error for an unknown stop slug")
	}
}

// The SPA-120 counterpart to TestCompileServices_journeyCrossesAMergedNode:
// board service A, cross to service B via a *declared* pair rather than
// proximity. The two crossing stops sit roughly 111 km apart (one degree of
// latitude) — nowhere near any merge or near-miss radius — so only the
// declared pair can connect them.
func TestCompileServices_journeyCrossesADeclaredInterchange(t *testing.T) {
	svcA := CompilableService{
		ID:      "svc-a",
		Route:   Route{Geometry: GeoLineString{Coordinates: [][]float64{{0, 0}, {0.01, 0}}}},
		Vehicle: physicsKinematics(),
		Stops: []CompilableStop{
			{Slug: "a--west", Name: "West", Lng: 0, Lat: 0},
			{Slug: "a--cross", Name: "Cross", Lng: 0.01, Lat: 0},
		},
	}
	svcB := CompilableService{
		ID:      "svc-b",
		Route:   Route{Geometry: GeoLineString{Coordinates: [][]float64{{1, 1}, {1, 1.01}}}},
		Vehicle: physicsKinematics(),
		Stops: []CompilableStop{
			{Slug: "b--cross", Name: "Cross", Lng: 1, Lat: 1},
			{Slug: "b--north", Name: "North", Lng: 1, Lat: 1.01},
		},
	}
	pairs := []InterchangePair{
		{A: StopIdentity{ServiceID: "svc-a", Slug: "a--cross"}, B: StopIdentity{ServiceID: "svc-b", Slug: "b--cross"}},
	}

	graph, err := CompileServices([]CompilableService{svcA, svcB}, pairs)
	if err != nil {
		t.Fatalf("CompileServices() error = %v, want nil", err)
	}

	secs, _, _, ok := graphDijkstra(&graph, "a--west", "b--north")
	if !ok {
		t.Fatal("no journey a--west -> b--north: the declared pair did not connect the two services")
	}
	if secs <= 0 {
		t.Errorf("journey secs = %d, want > 0", secs)
	}
	if len(graph.Merge.Clusters) != 1 || graph.Merge.Clusters[0].Key != "a--cross" {
		t.Errorf("clusters = %+v, want one keyed a--cross", graph.Merge.Clusters)
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

	got, report, _ := MergeColocatedStops(svcs, nil)
	keys := keysOf(got)
	if keys[0][0] != keys[1][0] {
		t.Fatalf("keys = %q and %q, want one shared key — the widened radius should have covered 333.6 m",
			keys[0][0], keys[1][0])
	}
	if len(report.Clusters) != 1 {
		t.Errorf("clusters = %+v, want exactly one realised merge", report.Clusters)
	}
}
