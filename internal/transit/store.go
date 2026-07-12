package transit

import (
	"embed"
	"fmt"
	"io/fs"

	"gopkg.in/yaml.v3"
)

//go:embed data
var dataFS embed.FS

// Store holds all transit domain data loaded from embedded YAML seed files.
// It is safe for concurrent read-only use after construction.
type Store struct {
	scenarios    []Scenario
	routes       []Route
	stations     []Station
	vehicleTypes []VehicleType
	services     []Service
	travelTimes  map[string]TravelTimes
}

// NewStore loads all embedded seed data and returns a ready Store.
func NewStore() (*Store, error) {
	s := &Store{
		travelTimes: make(map[string]TravelTimes),
	}

	entries, err := fs.ReadDir(dataFS, "data/scenarios")
	if err != nil {
		return nil, fmt.Errorf("transit: reading scenarios dir: %w", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := s.loadScenario(e.Name()); err != nil {
			return nil, fmt.Errorf("transit: loading scenario %q: %w", e.Name(), err)
		}
	}

	return s, nil
}

func (s *Store) loadScenario(slug string) error {
	base := "data/scenarios/" + slug

	var sc Scenario
	if err := unmarshalFile(dataFS, base+"/scenario.yaml", &sc); err != nil {
		return err
	}
	s.scenarios = append(s.scenarios, sc)

	var vts []VehicleType
	if err := unmarshalFile(dataFS, base+"/vehicle_types.yaml", &vts); err != nil {
		return err
	}
	s.vehicleTypes = append(s.vehicleTypes, vts...)

	var routes []Route
	if err := unmarshalFile(dataFS, base+"/routes.yaml", &routes); err != nil {
		return err
	}
	s.routes = append(s.routes, routes...)

	var stations []Station
	if err := unmarshalFile(dataFS, base+"/stations.yaml", &stations); err != nil {
		return err
	}
	s.stations = append(s.stations, stations...)

	var services []Service
	if err := unmarshalFile(dataFS, base+"/services.yaml", &services); err != nil {
		return err
	}
	s.services = append(s.services, services...)

	var tt TravelTimes
	if err := unmarshalFile(dataFS, base+"/travel_times.yaml", &tt); err != nil {
		return err
	}
	s.travelTimes[slug] = tt

	return nil
}

// GetScenarios returns all scenarios.
func (s *Store) GetScenarios() []Scenario {
	return s.scenarios
}

// GetScenarioBySlug returns the scenario with the given slug, or false if not found.
func (s *Store) GetScenarioBySlug(slug string) (Scenario, bool) {
	for _, sc := range s.scenarios {
		if sc.Slug == slug {
			return sc, true
		}
	}
	return Scenario{}, false
}

// GetRoutesByScenario returns all routes belonging to the given scenario ID.
func (s *Store) GetRoutesByScenario(scenarioID string) []Route {
	var out []Route
	for _, r := range s.routes {
		if r.ScenarioID == scenarioID {
			out = append(out, r)
		}
	}
	return out
}

// GetStationsByScenario returns all stations belonging to the given scenario ID.
func (s *Store) GetStationsByScenario(scenarioID string) []Station {
	var out []Station
	for _, st := range s.stations {
		if st.ScenarioID == scenarioID {
			out = append(out, st)
		}
	}
	return out
}

// GetServicesByScenario returns all active services belonging to the given scenario ID.
func (s *Store) GetServicesByScenario(scenarioID string) []Service {
	var out []Service
	for _, svc := range s.services {
		if svc.ScenarioID == scenarioID {
			out = append(out, svc)
		}
	}
	return out
}

// GetVehicleTypeByID returns the vehicle type with the given ID, or false if not found.
func (s *Store) GetVehicleTypeByID(id string) (VehicleType, bool) {
	for _, vt := range s.vehicleTypes {
		if vt.ID == id {
			return vt, true
		}
	}
	return VehicleType{}, false
}

// GetTravelTimes returns the travel-time matrix for the given scenario slug.
func (s *Store) GetTravelTimes(scenarioSlug string) (TravelTimes, bool) {
	tt, ok := s.travelTimes[scenarioSlug]
	return tt, ok
}

func unmarshalFile(fsys embed.FS, path string, v any) error {
	data, err := fsys.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	return nil
}
