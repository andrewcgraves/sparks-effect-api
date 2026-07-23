package transit

import (
	"sort"

	"github.com/andrewcgraves/sparks-effect-api/internal/physics"
)

// MergeRadiusM is the base radius for how close two stops of different
// services must be before they compile to the same graph node — that is,
// before a rider may change between those services there.
//
// It is measured on persisted snapped positions (see ServiceStopPoint), which
// makes it a statement about where the stops ended up on their alignments, not
// about where their authors put them. Those are not the same thing: SnapToRoute
// lets a stop move up to OffRouteThresholdM to reach its line, so two stops
// authored metres apart on crossing routes can land far enough apart to miss
// each other on this radius alone. That is why the radius actually applied is
// not this constant but effectiveMergeRadius (SPA-113): this is its floor, the
// distance two already-unmoved stops must still clear.
//
// The comparison is inclusive (distance <= radius), so a pair exactly on the
// boundary merges.
const MergeRadiusM = 50.0

// MaxMergeRadiusM ceilings effectiveMergeRadius. Without a ceiling the radius
// would grow without bound as offsets grow, eventually merging two stops whose
// only real connection is that both were snapped a long way. It is set to
// OffRouteThresholdM itself: that is the most either single stop was allowed
// to move to reach its own line, so letting the *pair's* combined benefit of
// the doubt exceed it would grant two stops together more slack than either
// was individually granted alone.
//
// This is independent of OffRouteThresholdM changing later — it is a separate
// decision that happens to reuse the same number today, not a derived
// constant, so a future change to one does not silently move the other.
const MaxMergeRadiusM = 500.0

// effectiveMergeRadius is the merge test's actual threshold for a specific
// pair: the base radius widened by both stops' snapping uncertainty, capped at
// MaxMergeRadiusM.
//
// SPA-113's reasoning: if a stop moved offsetM metres to reach its line, the
// authored point could have been anywhere within that distance of where it
// landed. Two stops authored at the same point could therefore still end up
// this far apart after independent snaps, so treating that separation as
// "could have been co-located" is not a relaxation of the rule, it is the same
// rule applied to the uncertainty snapping actually introduced. A stop that
// needed no correction (offsetM 0) contributes nothing, which is what makes
// MergeRadiusM alone the right answer for two stops already sitting on their
// lines.
func effectiveMergeRadius(offsetA, offsetB float64) float64 {
	r := MergeRadiusM + offsetA + offsetB
	if r > MaxMergeRadiusM {
		return MaxMergeRadiusM
	}
	return r
}

// NearMissRadiusM is how far out the near-miss report looks. Pairs of stops on
// different services that did not merge, but lie within this, are reported.
//
// 5x the merge radius: wide enough to cover the case that motivates the report
// — crossing alignments at a shared station sit routinely 50–150 m apart, so
// the stops a user meant as one interchange land just outside the merge and
// need naming — and narrow enough that the report stays a short list of
// plausible candidates rather than every pair in the scenario. Beyond ~250 m
// two stops are not a missed interchange; they are two stops.
const NearMissRadiusM = 5 * MergeRadiusM

// StopRef identifies one stop as it was before merging: which service it came
// from, the stable identity that service minted for it (StopSlugs), and its
// display name. It is what the merge report says "this" and "that" with.
type StopRef struct {
	ServiceID string `json:"service_id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
}

// StopCluster is one realised merge: several services' stops that compiled to
// a single graph node.
//
// Key is the node key the graph's edges actually name — the lexicographically
// smallest member slug. Names carries every distinct member name, in the same
// order as Members, so a caller can render "Transbay (also: Salesforce
// Center)" rather than showing one name and silently dropping the rest.
type StopCluster struct {
	Key     string    `json:"key"`
	Names   []string  `json:"names"`
	Members []StopRef `json:"members"`
}

// NearMiss is a pair of stops on different services that did not merge but came
// close enough that the author may well have meant them to.
//
// It exists because a failed merge is otherwise completely silent: the compile
// succeeds, the graph is simply smaller, and the isochrone reports a smaller
// world with nothing anywhere saying why. A near miss is not an error — plenty
// of stops are legitimately 80 m apart — so it is reported, not raised.
type NearMiss struct {
	A         StopRef `json:"a"`
	B         StopRef `json:"b"`
	DistanceM float64 `json:"distance_m"`
}

// MergeReport is what the merge did, in both directions: the merges it made
// and the merges it nearly made. An unwanted merge inflates reachability and a
// missed one deflates it, and neither is visible in the graph itself, so both
// are reported.
//
// Clusters holds only multi-member clusters. Every stop is in a cluster, but a
// singleton cluster is just a stop; reporting those would bury the merges in a
// list of non-events.
type MergeReport struct {
	Clusters   []StopCluster `json:"clusters,omitempty"`
	NearMisses []NearMiss    `json:"near_misses,omitempty"`
}

// MergeColocatedStops resolves interchange across a scenario's member
// services: it clusters stops that sit within effectiveMergeRadius of each
// other and rewrites each stop's Slug to its cluster's key, returning the
// rewritten services alongside a report of what it did.
//
// This is the whole of interchange in this system. There is no transfer edge
// type and no transfer table — graphDijkstra pools every ServiceGraph's edges
// into one adjacency map keyed by slug, so two services connect if and only if
// they emit an edge naming the same key. Since StopSlugs namespaces every
// stop identity by its owning service, nothing shares a key by default and a
// scenario's services would compile to disconnected components. Assigning the
// cluster key over the top is what makes a curated set of services a network.
//
// # The algorithm (decided on SPA-115)
//
// Collect every stop across every service, sort them into one total order, and
// walk that order. For each stop, test it against the *anchor* of each existing
// cluster in creation order; join the first cluster whose anchor is within
// effectiveMergeRadius (SPA-113: MergeRadiusM widened by the anchor's and the
// candidate's OffsetM, capped at MaxMergeRadiusM) and that does not already
// hold a stop of the same service. Otherwise start a new cluster anchored at
// this stop.
//
// Three properties follow, and each is why a simpler algorithm was rejected:
//
//   - **Order-independent.** The walk order is a property of the data (slug,
//     then service ID), not of the order services or rows arrived in. Naive
//     greedy over an arbitrary visit order gives genuinely different networks
//     for the same stops — visit A,B,C and get {A,B},{C}; visit C,B,A and get
//     {B,C},{A} — which would make a compile depend on a database's row order.
//
//   - **Non-transitive, by construction.** Membership is always measured
//     against the anchor, never against an arbitrary member, so chains cannot
//     propagate: with A–B and B–C each inside the radius but A–C outside, the
//     result is always {A,B} and {C}. Single-linkage merging would instead let
//     a line of stops 40 m apart collapse into one node spanning kilometres.
//
//   - **The key needs no separate pass.** Walking in ascending slug order means
//     a cluster's anchor is, necessarily, its lexicographically smallest
//     member — so the anchor *is* the key. Selecting the key separately would
//     be circular: the key would decide membership and membership the key.
//
// The accepted cost is that a stop can sit within the radius of a *non-anchor*
// member of a cluster it does not join. That is the unavoidable price of
// bounding chains, and it is not swept under the rug: such a pair is reported
// as a near miss like any other.
//
// # Clustering is cross-service only
//
// Two stops of one service never merge, however close. A lone service stopping
// twice within the radius is a loop, a switchback, or an authoring mistake —
// not an interchange — and merging it would delete a span, which is exactly
// what CompileServicePhysics' duplicate-slug check exists to refuse. So this
// can never be what puts a duplicate in front of that check, and that check
// does not relax to accommodate this.
//
// # Nothing here is persisted
//
// Cluster keys live for one compile. No Station table, no shared stop catalog:
// stops stay embedded on their services, which is the decision UserService was
// built around, and the merge is an artifact of assembling a graph rather than
// a fact about the world.
//
// The consequence is that a key is stable per compile, not across membership
// changes: adding a service whose stop slug sorts earlier moves the key to it.
// That is acceptable because the graph is a snapshot on a job row rather than a
// durable address — but nothing may deep-link a cluster key as though it were
// permanent.
//
// It also returns the graph's node set: one GraphNode per cluster, singletons
// included. Nodes are built here rather than re-derived later because the
// clusters are the node set — every cluster is a graph key and every graph key
// is a cluster — and this is the one place they exist. Reconstructing them
// after the fact would mean re-clustering, which the compiled graph exists
// precisely to avoid: the key is stable per compile, not across membership
// changes.
//
// # Name agreement does not widen the radius (SPA-114)
//
// Two same-named stops still merge on proximity alone, same as any other pair
// — a shared name does not lower the bar. Names are common ("Downtown") and
// meaningless past the merge/near-miss test's own scale, so folding them into
// the radius would let a name substitute for location precisely where the
// ticket that raised the question said it must not. The cheaper fix already
// covers the case that motivates this: a same-named pair just outside the
// radius reports as a near miss like any other, and the shared name is right
// there in the report (both StopCluster.Names and NearMiss.A/B.Name) for
// whoever reads it to act on. Reconsider only alongside a real signal for
// "same name, unrelated place" — none exists here — not as a standalone
// widening.
//
// # Explicit declared interchange is deferred (SPA-114 → SPA-120)
//
// The actual fix for intent — a scenario asserting stop X of service A is
// stop Y of service B, threshold-free — is out of scope here. It costs a data
// model addition and frontend work that this ticket's proximity-plus-report
// fix does not need, so it is deferred deliberately to SPA-120 rather than
// left an unexamined absence.
//
// Input is not mutated; the returned services carry fresh stop slices.
//
// pairs is SPA-120's declared-interchange override: a scenario's assertion
// that two stops, identified by (service ID, stop slug), are the same place
// regardless of distance. Declared pairs are folded into the clusters this
// proximity walk already produced (see foldDeclaredPairs) rather than
// consulted during the walk itself, so passing an empty pairs is
// byte-identical to this function before SPA-120, and a stop naming no
// declared pair merges, near-misses, or stays separate exactly as it always
// did.
func MergeColocatedStops(svcs []CompilableService, pairs []InterchangePair) ([]CompilableService, MergeReport, []GraphNode) {
	stops := flattenStops(svcs)
	clusters, clusterOf := clusterStops(stops)
	clusters, clusterOf = foldDeclaredPairs(stops, clusters, clusterOf, pairs)

	merged := make([]CompilableService, len(svcs))
	copy(merged, svcs)
	for i, svc := range svcs {
		merged[i].Stops = append([]CompilableStop(nil), svc.Stops...)
	}
	for i, s := range stops {
		merged[s.svcIdx].Stops[s.stopIdx].Slug = clusters[clusterOf[i]].key()
	}

	report := MergeReport{
		Clusters:   realisedClusters(clusters),
		NearMisses: nearMisses(stops, clusterOf),
	}
	return merged, report, clusterNodes(clusters)
}

// mergeStop is one stop lifted out of its service and given everything the
// merge reasons about: where it is, who it belongs to, and where to write its
// key back.
type mergeStop struct {
	svcIdx, stopIdx int
	ref             StopRef
	at              physics.Point
	offsetM         float64
}

// flattenStops collects every stop of every service into the single total
// order the walk depends on: slug ascending, then service ID.
//
// The service-ID tiebreak is not decoration. SPA-103 makes user-authored stop
// slugs globally unique by namespacing them, but the seeded model has no such
// guarantee — two seeded services calling at the same shared Station carry the
// identical station slug, which is precisely how express and local already
// interchange. Sorting on slug alone would leave those tied and the order
// decided by the input's arrangement, which is the order-dependence this whole
// ordering exists to remove. Service IDs are unique, and a single service
// cannot hold the same slug twice (CompileServicePhysics refuses it), so the
// pair is a total order over any input the compiler will accept.
func flattenStops(svcs []CompilableService) []mergeStop {
	var stops []mergeStop
	for i, svc := range svcs {
		for j, s := range svc.Stops {
			stops = append(stops, mergeStop{
				svcIdx:  i,
				stopIdx: j,
				ref:     StopRef{ServiceID: svc.ID, Slug: s.Slug, Name: s.Name},
				at:      physics.Point{Lng: s.Lng, Lat: s.Lat},
				offsetM: s.OffsetM,
			})
		}
	}
	sort.SliceStable(stops, func(a, b int) bool {
		if stops[a].ref.Slug != stops[b].ref.Slug {
			return stops[a].ref.Slug < stops[b].ref.Slug
		}
		return stops[a].ref.ServiceID < stops[b].ref.ServiceID
	})
	return stops
}

// pendingCluster is a cluster under construction. Members are appended in walk
// order, so members[0] is the anchor and, because the walk ascends by slug,
// also the lexicographically smallest member.
type pendingCluster struct {
	members  []mergeStop
	services map[string]bool
}

// key is the cluster's graph node key. Because the walk ascends by slug and
// members are appended in walk order, members[0] is both the anchor and the
// lexicographically smallest member — so the key needs no separate selection.
func (c pendingCluster) key() string { return c.members[0].ref.Slug }

// anchor is the position every candidate is measured against. It is members[0]
// and never changes as the cluster grows, which is what bounds chains:
// membership is tested against this one point, not against whichever member
// happens to be nearest.
func (c pendingCluster) anchor() physics.Point { return c.members[0].at }

// anchorOffsetM is the anchor's own OffsetM, needed alongside anchor() to
// compute effectiveMergeRadius for a candidate.
func (c pendingCluster) anchorOffsetM() float64 { return c.members[0].offsetM }

// clusterStops runs the greedy anchored walk over stops, which must already be
// in flattenStops' order. It returns the clusters in creation order — which,
// since each is created at its anchor, is ascending key order — and a parallel
// slice giving each stop's cluster index.
func clusterStops(stops []mergeStop) ([]pendingCluster, []int) {
	clusters := make([]pendingCluster, 0, len(stops))
	clusterOf := make([]int, len(stops))

	for i, s := range stops {
		joined := -1
		for ci := range clusters {
			if clusters[ci].services[s.ref.ServiceID] {
				continue
			}
			radius := effectiveMergeRadius(clusters[ci].anchorOffsetM(), s.offsetM)
			if physics.DistanceM(clusters[ci].anchor(), s.at) <= radius {
				joined = ci
				break
			}
		}
		if joined < 0 {
			clusters = append(clusters, pendingCluster{
				members:  []mergeStop{s},
				services: map[string]bool{s.ref.ServiceID: true},
			})
			clusterOf[i] = len(clusters) - 1
			continue
		}
		clusters[joined].members = append(clusters[joined].members, s)
		clusters[joined].services[s.ref.ServiceID] = true
		clusterOf[i] = joined
	}
	return clusters, clusterOf
}

// clusterNames returns a cluster's distinct member names in member order, so
// the key member's own name — the anchor, appended first — comes first.
//
// It is the single source of that ordered, deduplicated name list, shared by
// the merge report (StopCluster.Names) and the graph nodes (GraphNode.Names) so
// the two can never disagree about what a cluster is called. Two services
// calling the same place "Transbay" have stated one name, not two, and
// "Transbay (also: Transbay)" would be noise.
func clusterNames(c pendingCluster) []string {
	var names []string
	seen := make(map[string]bool, len(c.members))
	for _, m := range c.members {
		if seen[m.ref.Name] {
			continue
		}
		seen[m.ref.Name] = true
		names = append(names, m.ref.Name)
	}
	return names
}

// node projects a cluster onto the one addressable point the graph's edges
// name: its key, the anchor's persisted-snapped position — which is the key
// member's, and deliberately not a centroid (see GraphNode) — and the member
// names for display.
func (c pendingCluster) node() GraphNode {
	at := c.anchor()
	return GraphNode{Slug: c.key(), Lat: at.Lat, Lng: at.Lng, Names: clusterNames(c)}
}

// clusterNodes turns every cluster into one GraphNode, singletons included.
// Every cluster is a graph key and every graph key is a cluster, so this is
// exactly the node set the compiled edges address — one node per key, no
// dangling edge key and no orphan node. Singletons are kept precisely because a
// lone stop is still a node its own service's edges name; only realisedClusters
// drops them, because a singleton is not an interchange to report.
func clusterNodes(clusters []pendingCluster) []GraphNode {
	nodes := make([]GraphNode, len(clusters))
	for i, c := range clusters {
		nodes[i] = c.node()
	}
	return nodes
}

// realisedClusters reports the clusters that actually merged something.
func realisedClusters(clusters []pendingCluster) []StopCluster {
	var out []StopCluster
	for _, c := range clusters {
		if len(c.members) < 2 {
			continue
		}
		members := make([]StopRef, len(c.members))
		for i, m := range c.members {
			members[i] = m.ref
		}
		out = append(out, StopCluster{Key: c.key(), Names: clusterNames(c), Members: members})
	}
	return out
}

// nearMisses reports every pair of stops on different services that ended up in
// different clusters while lying within NearMissRadiusM of each other.
//
// Same-service pairs are excluded rather than reported as misses: they were
// never candidates to merge (see MergeColocatedStops), so calling them near
// misses would invite a user to fix something that is not broken.
//
// Pairs *inside* the merge radius that still failed to merge are included, and
// are the most interesting entries in the report: they are the anchored walk's
// accepted cost — a stop within reach of a non-anchor member — surfacing where
// it can be seen instead of silently shaping the graph.
//
// O(n^2) over the scenario's stops. At the scale of a curated set of authored
// services that is nothing; if it ever stops being nothing, the answer is a
// grid index over the same rule, not a different rule.
func nearMisses(stops []mergeStop, clusterOf []int) []NearMiss {
	var out []NearMiss
	for i := range stops {
		for j := i + 1; j < len(stops); j++ {
			if stops[i].ref.ServiceID == stops[j].ref.ServiceID {
				continue
			}
			if clusterOf[i] == clusterOf[j] {
				continue
			}
			d := physics.DistanceM(stops[i].at, stops[j].at)
			if d > NearMissRadiusM {
				continue
			}
			out = append(out, NearMiss{A: stops[i].ref, B: stops[j].ref, DistanceM: d})
		}
	}
	return out
}
