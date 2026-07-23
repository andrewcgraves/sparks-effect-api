package transit

// CompiledGraphData adapts one already-resolved compiled TransitGraph into
// IsochroneData, for computing an isochrone over a specific compile job's
// result rather than the seeded embedded store. scenarioSlug is accepted but
// ignored on both methods: the graph is already scoped to one scenario by the
// caller that built this value, so there is nothing left to resolve by slug.
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
