package transit

import (
	"container/heap"
	"context"
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
	graphs       map[string]*TransitGraph
}

// NewStore loads all embedded seed data and returns a ready Store.
func NewStore() (*Store, error) {
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
		if err := s.loadScenario(e.Name()); err != nil {
			return nil, fmt.Errorf("transit: loading scenario %q: %w", e.Name(), err)
		}
	}

	return s, nil
}

// LoadStore builds a read-optimized, compiled Store from a Repository. It reads
// every scenario's rows (routes, stations, services, travel-time segments) plus
// the global vehicle types, then compiles each scenario's in-memory TransitGraph.
// This is the persisted read path: rows in, isochrone-ready graph out.
func LoadStore(ctx context.Context, repo Repository) (*Store, error) {
	s := &Store{
		travelTimes: make(map[string]TravelTimes),
		graphs:      make(map[string]*TransitGraph),
	}

	vts, err := repo.ListVehicleTypes(ctx)
	if err != nil {
		return nil, fmt.Errorf("transit: loading vehicle types: %w", err)
	}
	s.vehicleTypes = vts

	scenarios, err := repo.ListScenarios(ctx)
	if err != nil {
		return nil, fmt.Errorf("transit: listing scenarios: %w", err)
	}

	for _, sc := range scenarios {
		routes, err := repo.ListRoutesByScenario(ctx, sc.ID)
		if err != nil {
			return nil, fmt.Errorf("transit: loading routes for %q: %w", sc.Slug, err)
		}
		stations, err := repo.ListStationsByScenario(ctx, sc.ID)
		if err != nil {
			return nil, fmt.Errorf("transit: loading stations for %q: %w", sc.Slug, err)
		}
		services, err := repo.ListServicesByScenario(ctx, sc.ID)
		if err != nil {
			return nil, fmt.Errorf("transit: loading services for %q: %w", sc.Slug, err)
		}
		tt, _, err := repo.GetTravelTimes(ctx, sc.Slug)
		if err != nil {
			return nil, fmt.Errorf("transit: loading travel times for %q: %w", sc.Slug, err)
		}

		s.scenarios = append(s.scenarios, sc)
		s.routes = append(s.routes, routes...)
		s.stations = append(s.stations, stations...)
		s.services = append(s.services, services...)
		s.travelTimes[sc.Slug] = tt

		g, err := Compile(sc, routes, stations, services, vts, tt)
		if err != nil {
			return nil, fmt.Errorf("transit: compiling %q: %w", sc.Slug, err)
		}
		s.graphs[sc.Slug] = g
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
	if err := unmarshalFile(dataFS, base+"/segment_run_times.yaml", &tt); err != nil {
		return err
	}
	s.travelTimes[slug] = tt

	g, err := Compile(sc, routes, stations, services, vts, tt)
	if err != nil {
		return err
	}
	s.graphs[slug] = g

	return nil
}

// Graph returns the compiled TransitGraph for a scenario slug.
func (s *Store) Graph(scenarioSlug string) (*TransitGraph, bool) {
	g, ok := s.graphs[scenarioSlug]
	return g, ok
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
		if svc.ScenarioID == scenarioID && svc.Active {
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

// GetTravelTimes returns the segment-based travel times for the given scenario slug.
func (s *Store) GetTravelTimes(scenarioSlug string) (TravelTimes, bool) {
	tt, ok := s.travelTimes[scenarioSlug]
	return tt, ok
}

// TravelTimeBetween returns the Dijkstra travel time in seconds, the boarding wait seconds,
// the boarding service ID, and reachability over the compiled TransitGraph. Returns false
// if the scenario is missing or no path exists between the stations.
func (s *Store) TravelTimeBetween(scenarioSlug, fromSlug, toSlug string) (seconds, waitSecs int, serviceID string, ok bool) {
	g, gOK := s.graphs[scenarioSlug]
	if !gOK {
		return 0, 0, "", false
	}
	if fromSlug == toSlug {
		return 0, 0, "", true
	}
	return graphDijkstra(g, fromSlug, toSlug)
}

type dijkNode struct {
	slug string
	secs int
}

type dijkHeap []dijkNode

func (h dijkHeap) Len() int           { return len(h) }
func (h dijkHeap) Less(i, j int) bool { return h[i].secs < h[j].secs }
func (h dijkHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *dijkHeap) Push(x any)        { *h = append(*h, x.(dijkNode)) }
func (h *dijkHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

func graphDijkstra(g *TransitGraph, from, to string) (int, int, string, bool) {
	type neighbor struct {
		slug      string
		secs      int
		serviceID string
		waitSecs  int
	}
	adj := make(map[string][]neighbor)
	for _, sg := range g.Services {
		for _, e := range sg.Edges {
			adj[e.FromSlug] = append(adj[e.FromSlug], neighbor{
				slug:      e.ToSlug,
				secs:      e.Seconds,
				serviceID: sg.ServiceID,
				waitSecs:  sg.WaitSecs,
			})
		}
	}

	type pathState struct {
		vehicleSecs int
		waitSecs    int
		serviceID   string
	}
	best := map[string]pathState{from: {}}
	h := &dijkHeap{{from, 0}}
	heap.Init(h)

	for h.Len() > 0 {
		cur := heap.Pop(h).(dijkNode)
		curState := best[cur.slug]
		if cur.secs > curState.vehicleSecs+curState.waitSecs {
			continue
		}
		if cur.slug == to {
			return curState.vehicleSecs, curState.waitSecs, curState.serviceID, true
		}
		for _, nb := range adj[cur.slug] {
			nextVehicle := curState.vehicleSecs + nb.secs
			nextWait := curState.waitSecs
			nextService := curState.serviceID
			if cur.slug == from {
				nextWait = nb.waitSecs
				nextService = nb.serviceID
			}
			nextTotal := nextVehicle + nextWait
			if prev, seen := best[nb.slug]; !seen || nextTotal < prev.vehicleSecs+prev.waitSecs {
				best[nb.slug] = pathState{nextVehicle, nextWait, nextService}
				heap.Push(h, dijkNode{nb.slug, nextTotal})
			}
		}
	}
	return 0, 0, "", false
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
