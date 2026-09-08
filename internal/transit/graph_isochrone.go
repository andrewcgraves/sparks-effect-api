package transit

type CompiledGraphData struct {
	Graph *TransitGraph
}

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

func (d CompiledGraphData) TravelTimeBetween(_, fromSlug, toSlug string) (seconds, waitSecs int, serviceID string, ok bool) {
	if d.Graph == nil {
		return 0, 0, "", false
	}
	if fromSlug == toSlug {
		return 0, 0, "", true
	}
	return graphDijkstra(d.Graph, fromSlug, toSlug)
}
