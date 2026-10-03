package transit

import (
	"context"
	"embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed data
var dataFS embed.FS

type Store struct {
	scenarios    []Scenario
	routes       []Route
	stations     []Station
	vehicleTypes []VehicleType
	services     []Service
	travelTimes  map[string]TravelTimes
	graphs       map[string]*TransitGraph
}

func NewStore(boardingWait BoardingWaitPolicy) (*Store, error) {
	seeds, err := loadEmbeddedScenarios()
	if err != nil {
		return nil, err
	}

	s := &Store{
		travelTimes: make(map[string]TravelTimes, len(seeds)),
		graphs:      make(map[string]*TransitGraph, len(seeds)),
	}
	for _, seed := range seeds {
		if err := s.addScenario(seed, boardingWait); err != nil {
			return nil, fmt.Errorf("transit: loading scenario %q: %w", seed.scenario.Slug, err)
		}
	}
	return s, nil
}

type StoreSource interface {
	ListVehicleTypes(ctx context.Context) ([]VehicleType, error)
	ListCuratedScenarios(ctx context.Context) ([]Scenario, error)
	ListRoutesByScenario(ctx context.Context, scenarioID string) ([]Route, error)
	ListStationsByScenario(ctx context.Context, scenarioID string) ([]Station, error)
	ListServicesByScenario(ctx context.Context, scenarioID string) ([]Service, error)
	GetTravelTimes(ctx context.Context, scenarioSlug string) (TravelTimes, bool, error)
}

func LoadStore(ctx context.Context, src StoreSource) (*Store, error) {
	s := &Store{
		travelTimes: make(map[string]TravelTimes),
	}

	vts, err := src.ListVehicleTypes(ctx)
	if err != nil {
		return nil, fmt.Errorf("transit: loading vehicle types: %w", err)
	}
	s.vehicleTypes = vts

	scenarios, err := src.ListCuratedScenarios(ctx)
	if err != nil {
		return nil, fmt.Errorf("transit: listing scenarios: %w", err)
	}

	for _, sc := range scenarios {
		routes, err := src.ListRoutesByScenario(ctx, sc.ID)
		if err != nil {
			return nil, fmt.Errorf("transit: loading routes for %q: %w", sc.Slug, err)
		}
		stations, err := src.ListStationsByScenario(ctx, sc.ID)
		if err != nil {
			return nil, fmt.Errorf("transit: loading stations for %q: %w", sc.Slug, err)
		}
		services, err := src.ListServicesByScenario(ctx, sc.ID)
		if err != nil {
			return nil, fmt.Errorf("transit: loading services for %q: %w", sc.Slug, err)
		}
		tt, _, err := src.GetTravelTimes(ctx, sc.Slug)
		if err != nil {
			return nil, fmt.Errorf("transit: loading travel times for %q: %w", sc.Slug, err)
		}

		s.scenarios = append(s.scenarios, sc)
		s.routes = append(s.routes, routes...)
		s.stations = append(s.stations, stations...)
		s.services = append(s.services, services...)
		s.travelTimes[sc.Slug] = tt
	}

	return s, nil
}

func (s *Store) addScenario(seed embeddedScenario, boardingWait BoardingWaitPolicy) error {
	s.scenarios = append(s.scenarios, seed.scenario)
	s.vehicleTypes = append(s.vehicleTypes, seed.vehicleTypes...)
	s.routes = append(s.routes, seed.routes...)
	s.stations = append(s.stations, seed.stations...)
	s.services = append(s.services, seed.services...)
	s.travelTimes[seed.scenario.Slug] = seed.travelTimes

	g, err := Compile(seed.scenario, seed.routes, seed.stations, seed.services, seed.vehicleTypes, seed.travelTimes, boardingWait)
	if err != nil {
		return err
	}
	s.graphs[seed.scenario.Slug] = g
	return nil
}

func (s *Store) GetScenarios() []Scenario {
	return s.scenarios
}

func (s *Store) GetScenarioBySlug(slug string) (Scenario, bool) {
	for _, sc := range s.scenarios {
		if sc.Slug == slug {
			return sc, true
		}
	}
	return Scenario{}, false
}

func (s *Store) GetRoutesByScenario(scenarioID string) []Route {
	var out []Route
	for _, r := range s.routes {
		if r.ScenarioID != nil && *r.ScenarioID == scenarioID {
			out = append(out, r)
		}
	}
	return out
}

func (s *Store) GetStationsByScenario(scenarioID string) []Station {
	var out []Station
	for _, st := range s.stations {
		if st.ScenarioID == scenarioID {
			out = append(out, st)
		}
	}
	return out
}

func (s *Store) GetServicesByScenario(scenarioID string) []Service {
	var out []Service
	for _, svc := range s.services {
		if svc.ScenarioID == scenarioID && svc.Active {
			out = append(out, svc)
		}
	}
	return out
}

func (s *Store) GetVehicleTypeByID(id string) (VehicleType, bool) {
	for _, vt := range s.vehicleTypes {
		if vt.ID == id {
			return vt, true
		}
	}
	return VehicleType{}, false
}

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
