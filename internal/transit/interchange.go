package transit

import (
	"fmt"
	"sort"
)

type StopIdentity struct {
	ServiceID string `json:"service_id"`
	Slug      string `json:"slug"`
}

type InterchangePair struct {
	A StopIdentity `json:"a"`
	B StopIdentity `json:"b"`
}

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
