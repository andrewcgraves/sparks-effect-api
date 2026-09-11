package transit

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

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
	s := &Store{
		travelTimes: make(map[string]TravelTimes),
		graphs:      make(map[string]*TransitGraph),
	}

	entries, err := fs.ReadDir(dataFS, "data/scenarios")
	if err != nil {
		return nil, fmt.Errorf("transit: reading scenarios dir: %w", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := s.loadScenario(e.Name(), boardingWait); err != nil {
			return nil, fmt.Errorf("transit: loading scenario %q: %w", e.Name(), err)
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

func (s *Store) loadScenario(slug string, boardingWait BoardingWaitPolicy) error {
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
	for i, svc := range services {
		if svc.BoardingWait != nil {
			if _, err := svc.BoardingWait.Parse(); err != nil {
				return fmt.Errorf("service %q: %w", svc.ID, err)
			}
		}
		services[i] = svc
	}
	s.services = append(s.services, services...)

	var tt TravelTimes
	if err := unmarshalFile(dataFS, base+"/segment_run_times.yaml", &tt); err != nil {
		return err
	}
	if err := validateSegmentRoutes(routes, tt); err != nil {
		return err
	}
	s.travelTimes[slug] = tt

	g, err := Compile(sc, routes, stations, services, vts, tt, boardingWait)
	if err != nil {
		return err
	}
	s.graphs[slug] = g

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
