package transit

// Node is one addressable point in an isochrone-ready graph: the slug an Edge
// or TravelTimeBetween call names, plus the position an isochrone is plotted
// from.
type Node struct {
	Slug string
	Lat  float64
	Lng  float64
}

// IsochroneData is the read-only view of a scenario an isochrone is computed
// from, narrowed to exactly what that needs (SPA-83 decision 3): Nodes
// collapses scenario resolution and station lookup into the one thing that
// two-step ever accomplished, and TravelTimeBetween answers the graph search.
//
// Since SPA-182 nothing in this repository computes an isochrone — the worker
// does, over the graph the queue message carries — so this seam has no live
// request path behind it any more. It is kept as the equivalence harness the
// two data sources are pinned against: *Store reads the embedded seed data and
// CompiledGraphData reads a compile job's result, and SPA-181's acceptance
// test asserts they answer identically for every station pair. That comparison
// is what guarantees a compiled graph still describes the scenario it was
// compiled from, and it needs both implementations to keep existing.
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
