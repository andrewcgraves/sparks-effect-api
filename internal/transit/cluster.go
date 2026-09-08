package transit

import (
	"sort"

	"github.com/andrewcgraves/sparks-effect-api/internal/physics"
)

const MergeRadiusM = 50.0

const MaxMergeRadiusM = 500.0

func effectiveMergeRadius(offsetA, offsetB float64) float64 {
	r := MergeRadiusM + offsetA + offsetB
	if r > MaxMergeRadiusM {
		return MaxMergeRadiusM
	}
	return r
}

const NearMissRadiusM = 5 * MergeRadiusM

type StopRef struct {
	ServiceID string `json:"service_id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
}

type StopCluster struct {
	Key     string    `json:"key"`
	Names   []string  `json:"names"`
	Members []StopRef `json:"members"`
}

type NearMiss struct {
	A         StopRef `json:"a"`
	B         StopRef `json:"b"`
	DistanceM float64 `json:"distance_m"`
}

type MergeReport struct {
	Clusters   []StopCluster `json:"clusters,omitempty"`
	NearMisses []NearMiss    `json:"near_misses,omitempty"`
}

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

type mergeStop struct {
	svcIdx, stopIdx int
	ref             StopRef
	at              physics.Point
	offsetM         float64
}

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

type pendingCluster struct {
	members  []mergeStop
	services map[string]bool
}

func (c pendingCluster) key() string { return c.members[0].ref.Slug }

func (c pendingCluster) anchor() physics.Point { return c.members[0].at }

func (c pendingCluster) anchorOffsetM() float64 { return c.members[0].offsetM }

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

func (c pendingCluster) node() GraphNode {
	at := c.anchor()
	return GraphNode{Slug: c.key(), Lat: at.Lat, Lng: at.Lng, Names: clusterNames(c)}
}

func clusterNodes(clusters []pendingCluster) []GraphNode {
	nodes := make([]GraphNode, len(clusters))
	for i, c := range clusters {
		nodes[i] = c.node()
	}
	return nodes
}

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
