package transit

import (
	"fmt"
	"sort"
)

// StopIdentity names one stop the way StopSlugs identifies it: which service
// it belongs to and the slug that service minted for it. It is how a
// declared interchange pair names "this stop" — the pre-merge identity, not
// the post-merge graph key MergeColocatedStops assigns.
type StopIdentity struct {
	ServiceID string `json:"service_id"`
	Slug      string `json:"slug"`
}

// InterchangePair is a scenario's explicit assertion that two stops, each on
// a different service, are the same place (SPA-120).
//
// Proximity — MergeColocatedStops' anchored walk, widened by snapping offset
// (SPA-113) — is a geometric proxy for a rider's intent, not a statement of
// it: two stops snapped onto crossing alignments can land anywhere from
// metres to kilometres apart, and only the scenario's author knows which
// gap, if any, was ever meant to be one interchange. A declared pair is that
// intent stated directly rather than inferred from distance.
//
// It is scoped to the scenario that declares it and names two existing
// embedded stops by identity, not a shared Station row — the "embedded
// stops, no shared catalog" decision UserService was built around is
// unaffected: this is a per-scenario assertion about two services, not a
// durable place in a catalog.
type InterchangePair struct {
	A StopIdentity `json:"a"`
	B StopIdentity `json:"b"`
}

// validateInterchangePairs checks a scenario's declared pairs against the
// services actually being compiled — the one place both a pair's claimed
// identities and the real stop list are in scope together. It runs once,
// at the CompileServices seam, before MergeColocatedStops ever sees the
// pairs; MergeColocatedStops trusts them and silently ignores anything
// unknown (see foldDeclaredPairs).
//
// A pair naming the same service on both sides is rejected outright rather
// than left to surface downstream as a duplicate-slug compile failure:
// CompileServicePhysics' duplicate-slug check exists to catch two of one
// service's own stops colliding on a key, and letting a declared pair be the
// thing that puts a duplicate in front of it would report the wrong cause.
func validateInterchangePairs(svcs []CompilableService, pairs []InterchangePair) error {
	if len(pairs) == 0 {
		return nil
	}

	known := make(map[StopIdentity]bool)
	for _, svc := range svcs {
		for _, s := range svc.Stops {
			known[StopIdentity{ServiceID: svc.ID, Slug: s.Slug}] = true
		}
	}

	for i, p := range pairs {
		if p.A.ServiceID == p.B.ServiceID {
			return fmt.Errorf("compile: interchange pair %d declares two stops on the same service %q", i, p.A.ServiceID)
		}
		if !known[p.A] {
			return fmt.Errorf("compile: interchange pair %d references unknown stop %q on service %q", i, p.A.Slug, p.A.ServiceID)
		}
		if !known[p.B] {
			return fmt.Errorf("compile: interchange pair %d references unknown stop %q on service %q", i, p.B.Slug, p.B.ServiceID)
		}
	}
	return nil
}

// foldDeclaredPairs folds a scenario's declared interchange pairs into
// clusterStops' proximity-only output: for each pair, the two clusters its
// stops ended up in are unioned into one, regardless of the distance between
// them.
//
// This runs as a second pass over clusters clusterStops has already decided
// by proximity alone — it does not touch the anchored walk itself. That is
// what keeps an undeclared pair's outcome exactly what it always was: a stop
// that names no declared pair merges, near-misses, or stays separate exactly
// as it would if pairs were empty, because the cluster it starts in, and
// every distance comparison that put it there, is unchanged. Only clusters a
// declared pair actually names ever get folded together.
//
// A pair whose two stops already share a cluster — already within the
// proximity radius, or already folded together by an earlier pair — is a
// silent no-op: union-find only ever narrows the group count, never errors.
//
// Folding operates on whole clusters, not just the two named stops: if a
// declared stop already sits in a multi-member proximity cluster, every
// other member of that cluster is folded in too. This is deliberate rather
// than an accidental consequence of unioning cluster indices — a declared
// pair asserts that a *place* is shared, and a stop already proximity-merged
// with the declared one already shares that place, whether or not it was
// itself named. It is still true that no *other*, disjoint cluster is
// touched: only the clusters a declared pair's named stops actually belong
// to are ever folded together.
//
// Unknown stop identities are ignored here rather than rejected: validating
// that a pair names two real stops is validateInterchangePairs' job, run
// once by CompileServices before this ever sees the pairs.
func foldDeclaredPairs(stops []mergeStop, clusters []pendingCluster, clusterOf []int, pairs []InterchangePair) ([]pendingCluster, []int) {
	if len(pairs) == 0 {
		return clusters, clusterOf
	}

	index := make(map[StopIdentity]int, len(stops))
	for i, s := range stops {
		index[StopIdentity{ServiceID: s.ref.ServiceID, Slug: s.ref.Slug}] = i
	}

	parent := make([]int, len(clusters))
	for i := range parent {
		parent[i] = i
	}
	for _, p := range pairs {
		ia, ok := index[p.A]
		if !ok {
			continue
		}
		ib, ok := index[p.B]
		if !ok {
			continue
		}
		unionClusters(parent, clusterOf[ia], clusterOf[ib])
	}

	rootOf := make([]int, len(clusters))
	for i := range clusters {
		rootOf[i] = findCluster(parent, i)
	}

	// Visiting original cluster indices in ascending order and emitting a
	// group the first time its root is seen makes the folded output — and
	// so which member ends up smallest-slug-first within a folded cluster —
	// a function of which clusters ended up together, never of the order
	// pairs were declared in or the order union-find happened to process
	// them.
	folded := make([]pendingCluster, 0, len(clusters))
	foldedIndexOf := make(map[int]int, len(clusters))
	for i := range clusters {
		root := rootOf[i]
		if _, seen := foldedIndexOf[root]; seen {
			continue
		}
		foldedIndexOf[root] = len(folded)
		folded = append(folded, foldGroup(clusters, rootOf, root))
	}

	finalClusterOf := make([]int, len(clusterOf))
	for i, ci := range clusterOf {
		finalClusterOf[i] = foldedIndexOf[rootOf[ci]]
	}
	return folded, finalClusterOf
}

// foldGroup merges every cluster sharing root into one, re-sorting members
// into flattenStops' order so the result still holds clusterStops' invariant
// — members[0] is the lexicographically smallest slug — regardless of how
// many original clusters or declared pairs fed into it.
func foldGroup(clusters []pendingCluster, rootOf []int, root int) pendingCluster {
	var members []mergeStop
	services := make(map[string]bool)
	for i, c := range clusters {
		if rootOf[i] != root {
			continue
		}
		members = append(members, c.members...)
		for svc := range c.services {
			services[svc] = true
		}
	}
	sort.SliceStable(members, func(a, b int) bool {
		if members[a].ref.Slug != members[b].ref.Slug {
			return members[a].ref.Slug < members[b].ref.Slug
		}
		return members[a].ref.ServiceID < members[b].ref.ServiceID
	})
	return pendingCluster{members: members, services: services}
}

// findCluster and unionClusters are a minimal path-compressed union-find
// over cluster indices, scoped to foldDeclaredPairs' one pass.
func findCluster(parent []int, x int) int {
	for parent[x] != x {
		parent[x] = parent[parent[x]]
		x = parent[x]
	}
	return x
}

func unionClusters(parent []int, a, b int) {
	ra, rb := findCluster(parent, a), findCluster(parent, b)
	if ra != rb {
		parent[ra] = rb
	}
}
