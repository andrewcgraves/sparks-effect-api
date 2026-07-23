package transit

// Node is one addressable point in an isochrone-ready graph: the slug an Edge
// or TravelTimeBetween call names, plus the position the chainer needs.
type Node struct {
	Slug string
	Lat  float64
	Lng  float64
}

// IsochroneData is the read-only seam the isochrone chainer requires, narrowed
// to exactly what it reads (SPA-83 decision 3). Nodes collapses scenario
// resolution and station lookup into the one thing that two-step ever
// accomplished; TravelTimeBetween is unchanged. *Store satisfies this against
// the seeded scenario data; a compiled user-authored graph (CompiledGraphData)
// is a second, narrower implementation with no scenario or station rows to
// fabricate.
type IsochroneData interface {
	Nodes(scenarioSlug string) ([]Node, bool)
	TravelTimeBetween(scenarioSlug, fromSlug, toSlug string) (seconds, waitSecs int, serviceID string, ok bool)
}

// Nodes adapts the seeded scenario/station lookup onto IsochroneData: the
// scenario resolves the caller's slug to an ID, and its stations become nodes
// keyed by slug.
func (s *Store) Nodes(scenarioSlug string) ([]Node, bool) {
	sc, ok := s.GetScenarioBySlug(scenarioSlug)
	if !ok {
		return nil, false
	}
	stations := s.GetStationsByScenario(sc.ID)
	nodes := make([]Node, len(stations))
	for i, st := range stations {
		nodes[i] = Node{Slug: st.Slug, Lat: st.Location.Coordinates[1], Lng: st.Location.Coordinates[0]}
	}
	return nodes, true
}
