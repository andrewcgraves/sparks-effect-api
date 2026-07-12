package transit

// TransitData is the read-only seam the isochrone chainer requires.
// *Store satisfies this interface.
type TransitData interface {
	GetScenarioBySlug(slug string) (Scenario, bool)
	GetStationsByScenario(scenarioID string) []Station
	GetServicesByScenario(scenarioID string) []Service
	TravelTimeBetween(scenarioSlug, fromSlug, toSlug string) (int, bool)
}
