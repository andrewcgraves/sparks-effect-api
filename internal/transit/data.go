package transit

// TransitData is the read-only seam the isochrone chainer requires.
// TravelTimeBetween returns travel seconds, boarding wait seconds, boarding service ID,
// and reachability. *Store satisfies this interface.
type TransitData interface {
	GetScenarioBySlug(slug string) (Scenario, bool)
	GetStationsByScenario(scenarioID string) []Station
	GetServicesByScenario(scenarioID string) []Service
	TravelTimeBetween(scenarioSlug, fromSlug, toSlug string) (seconds, waitSecs int, serviceID string, ok bool)
}
