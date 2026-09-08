package transit

type Node struct {
	Slug string
	Lat  float64
	Lng  float64
}

type IsochroneData interface {
	Nodes(scenarioSlug string) ([]Node, bool)
	TravelTimeBetween(scenarioSlug, fromSlug, toSlug string) (seconds, waitSecs int, serviceID string, ok bool)
}

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
