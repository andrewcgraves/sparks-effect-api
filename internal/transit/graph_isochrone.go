package transit

// CompiledGraphData adapts one already-resolved compiled TransitGraph into
// IsochroneData: the graph's own view of itself, as against *Store's view of
// the seed data it was compiled from. scenarioSlug is accepted but ignored on
// both methods — the graph is already scoped to one scenario by the caller that
// built this value, so there is nothing left to resolve by slug.
//
// It has no production caller since SPA-182 moved isochrone computation out of
// this repository. It survives as one half of the equivalence test described on
// IsochroneData, which is the check that a compiled graph and the seed data
// still agree.
type CompiledGraphData struct {
	Graph *TransitGraph
}

// Nodes reports the graph's own node set, carrying the slug, position, and
// display names SPA-111 attached at compile time.
func (d CompiledGraphData) Nodes(_ string) ([]Node, bool) {
	if d.Graph == nil {
		return nil, false
	}
	nodes := make([]Node, len(d.Graph.Nodes))
	for i, n := range d.Graph.Nodes {
		nodes[i] = Node{Slug: n.Slug, Lat: n.Lat, Lng: n.Lng}
	}
	return nodes, true
}

// TravelTimeBetween runs the same Dijkstra search Store.TravelTimeBetween
// uses, over this graph instead of a seeded scenario's.
func (d CompiledGraphData) TravelTimeBetween(_, fromSlug, toSlug string) (seconds, waitSecs int, serviceID string, ok bool) {
	if d.Graph == nil {
		return 0, 0, "", false
	}
	if fromSlug == toSlug {
		return 0, 0, "", true
	}
	return graphDijkstra(d.Graph, fromSlug, toSlug)
}
