package transit

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"sync"
	"unicode/utf8"
)

type SeedSink interface {
	ListCuratedScenarios(ctx context.Context) ([]Scenario, error)
	CreateScenario(ctx context.Context, sc Scenario) error
	CreateVehicleType(ctx context.Context, vt VehicleType) error
	CreateRoute(ctx context.Context, r Route) error
	CreateStation(ctx context.Context, st Station) error
	CreateService(ctx context.Context, svc Service) error
	AddServiceToScenario(ctx context.Context, scenarioID, serviceID string) error
	UpsertTravelTimes(ctx context.Context, tt TravelTimes) error
}

func SeedIfEmpty(ctx context.Context, sink SeedSink) (bool, error) {
	existing, err := sink.ListCuratedScenarios(ctx)
	if err != nil {
		return false, fmt.Errorf("transit: checking for existing scenarios: %w", err)
	}
	if len(existing) > 0 {
		return false, nil
	}
	if err := SeedFromEmbedded(ctx, sink); err != nil {
		return false, err
	}
	return true, nil
}

func SeedFromEmbedded(ctx context.Context, sink SeedSink) error {
	seeds, err := loadEmbeddedScenarios()
	if err != nil {
		return err
	}
	for _, seed := range seeds {
		if err := writeEmbeddedScenario(ctx, sink, seed); err != nil {
			return fmt.Errorf("transit: seeding scenario %q: %w", seed.scenario.Slug, err)
		}
	}
	return nil
}

type embeddedScenario struct {
	scenario     Scenario
	vehicleTypes []VehicleType
	routes       []Route
	stations     []Station
	services     []Service
	travelTimes  TravelTimes
}

var (
	embeddedOnce   sync.Once
	embeddedParsed []embeddedScenario
	embeddedErr    error
)

func loadEmbeddedScenarios() ([]embeddedScenario, error) {
	// The scenario YAML is compiled into the binary, and routes.yaml alone is
	// hundreds of kilobytes. Decoding it dominates every NewStore, seed, and
	// reconcile call, and tests do those on nearly every case. Parse once per
	// process and hand each caller a copy. Each caller keeps the slices it
	// was given: the store holds them, and seed writes them. Sharing the
	// cached parse would alias geometry and the other reference fields
	// across calls.
	embeddedOnce.Do(func() {
		embeddedParsed, embeddedErr = readEmbeddedScenarios()
	})
	if embeddedErr != nil {
		return nil, embeddedErr
	}
	return cloneEmbeddedScenarios(embeddedParsed), nil
}

func readEmbeddedScenarios() ([]embeddedScenario, error) {
	entries, err := fs.ReadDir(dataFS, "data/scenarios")
	if err != nil {
		return nil, fmt.Errorf("transit: reading scenarios dir: %w", err)
	}
	var out []embeddedScenario
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		seed, err := loadEmbeddedScenario(e.Name())
		if err != nil {
			return nil, fmt.Errorf("transit: loading scenario %q: %w", e.Name(), err)
		}
		out = append(out, seed)
	}
	return out, nil
}

func loadEmbeddedScenario(slug string) (embeddedScenario, error) {
	base := "data/scenarios/" + slug
	var seed embeddedScenario
	if err := unmarshalFile(dataFS, base+"/scenario.yaml", &seed.scenario); err != nil {
		return embeddedScenario{}, err
	}
	if err := validateScenarioSeed(seed.scenario); err != nil {
		return embeddedScenario{}, err
	}
	if err := unmarshalFile(dataFS, base+"/vehicle_types.yaml", &seed.vehicleTypes); err != nil {
		return embeddedScenario{}, err
	}
	if err := unmarshalFile(dataFS, base+"/routes.yaml", &seed.routes); err != nil {
		return embeddedScenario{}, err
	}
	if err := unmarshalFile(dataFS, base+"/stations.yaml", &seed.stations); err != nil {
		return embeddedScenario{}, err
	}
	if err := unmarshalFile(dataFS, base+"/services.yaml", &seed.services); err != nil {
		return embeddedScenario{}, err
	}
	for _, svc := range seed.services {
		if svc.BoardingWait != nil {
			if _, err := svc.BoardingWait.Parse(); err != nil {
				return embeddedScenario{}, fmt.Errorf("service %q: %w", svc.ID, err)
			}
		}
	}
	if err := unmarshalFile(dataFS, base+"/segment_run_times.yaml", &seed.travelTimes); err != nil {
		return embeddedScenario{}, err
	}
	if err := validateSegmentRoutes(seed.routes, seed.travelTimes); err != nil {
		return embeddedScenario{}, err
	}
	return seed, nil
}

func writeEmbeddedScenario(ctx context.Context, sink SeedSink, seed embeddedScenario) error {
	if err := sink.CreateScenario(ctx, seed.scenario); err != nil {
		return fmt.Errorf("creating scenario: %w", err)
	}
	for _, vt := range seed.vehicleTypes {
		if err := sink.CreateVehicleType(ctx, vt); err != nil {
			return fmt.Errorf("creating vehicle type %q: %w", vt.ID, err)
		}
	}
	for _, r := range seed.routes {
		if err := sink.CreateRoute(ctx, r); err != nil {
			return fmt.Errorf("creating route %q: %w", r.ID, err)
		}
	}
	for _, st := range seed.stations {
		if err := sink.CreateStation(ctx, st); err != nil {
			return fmt.Errorf("creating station %q: %w", st.ID, err)
		}
	}
	for _, svc := range seed.services {
		if err := sink.CreateService(ctx, svc); err != nil {
			return fmt.Errorf("creating service %q: %w", svc.ID, err)
		}
		if err := sink.AddServiceToScenario(ctx, svc.ScenarioID, svc.ID); err != nil {
			return fmt.Errorf("linking service %q to scenario: %w", svc.ID, err)
		}
	}
	if err := sink.UpsertTravelTimes(ctx, seed.travelTimes); err != nil {
		return fmt.Errorf("upserting travel times: %w", err)
	}
	return nil
}

func validateScenarioSeed(sc Scenario) error {
	// A curated scenario's subtext shares the bound UserService.Validate()
	// holds an authored service's to. Seed YAML has no client to read a fault,
	// so breaking it fails the load, which fails boot and the test suite.
	if utf8.RuneCountInString(sc.Subtext) > MaxSubtextChars {
		return fmt.Errorf("scenario %q: subtext must be at most %d characters", sc.Slug, MaxSubtextChars)
	}
	return nil
}

func validateSegmentRoutes(routes []Route, tt TravelTimes) error {
	known := make(map[string]bool, len(routes))
	for _, rt := range routes {
		known[rt.ID] = true
	}
	for _, seg := range tt.Segments {
		if !known[seg.RouteID] {
			return fmt.Errorf(
				"transit: segment %s→%s names route %q, which is not a route of scenario %q",
				seg.FromSlug, seg.ToSlug, seg.RouteID, tt.ScenarioSlug)
		}
		if seg.ReverseRunSeconds != nil && *seg.ReverseRunSeconds <= 0 {
			return fmt.Errorf(
				"transit: segment %s→%s has non-positive reverse_run_seconds",
				seg.FromSlug, seg.ToSlug)
		}
	}
	return nil
}

func cloneEmbeddedScenarios(in []embeddedScenario) []embeddedScenario {
	out := make([]embeddedScenario, len(in))
	for i := range in {
		out[i] = cloneEmbeddedScenario(in[i])
	}
	return out
}

func cloneEmbeddedScenario(in embeddedScenario) embeddedScenario {
	return embeddedScenario{
		scenario:     cloneScenario(in.scenario),
		vehicleTypes: slices.Clone(in.vehicleTypes),
		routes:       cloneRoutes(in.routes),
		stations:     cloneStations(in.stations),
		services:     cloneServices(in.services),
		travelTimes:  cloneTravelTimes(in.travelTimes),
	}
}

func cloneScenario(sc Scenario) Scenario {
	sc.OwnerID = cloneStringPtr(sc.OwnerID)
	return sc
}

func cloneRoutes(in []Route) []Route {
	if in == nil {
		return nil
	}
	out := make([]Route, len(in))
	for i := range in {
		out[i] = cloneRoute(in[i])
	}
	return out
}

func cloneRoute(r Route) Route {
	r.ScenarioID = cloneStringPtr(r.ScenarioID)
	r.OwnerID = cloneStringPtr(r.OwnerID)
	r.Geometry = cloneLine(r.Geometry)
	r.Segments = slices.Clone(r.Segments)
	return r
}

func cloneStations(in []Station) []Station {
	if in == nil {
		return nil
	}
	out := make([]Station, len(in))
	for i := range in {
		out[i] = cloneStation(in[i])
	}
	return out
}

func cloneStation(st Station) Station {
	st.OwnerID = cloneStringPtr(st.OwnerID)
	st.Location = cloneGeoPoint(st.Location)
	if st.RoutingLocation != nil {
		p := cloneGeoPoint(*st.RoutingLocation)
		st.RoutingLocation = &p
	}
	return st
}

func cloneServices(in []Service) []Service {
	if in == nil {
		return nil
	}
	out := make([]Service, len(in))
	for i := range in {
		out[i] = cloneService(in[i])
	}
	return out
}

func cloneService(svc Service) Service {
	svc.OwnerID = cloneStringPtr(svc.OwnerID)
	svc.Stops = cloneStops(svc.Stops)
	svc.FrequencyWindows = slices.Clone(svc.FrequencyWindows)
	svc.BoardingWait = cloneBoardingWait(svc.BoardingWait)
	return svc
}

func cloneStops(in []ServiceStop) []ServiceStop {
	if in == nil {
		return nil
	}
	out := make([]ServiceStop, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].DwellS = cloneIntPtr(in[i].DwellS)
	}
	return out
}

func cloneTravelTimes(tt TravelTimes) TravelTimes {
	if tt.Segments == nil {
		return tt
	}
	tt.Segments = slices.Clone(tt.Segments)
	for i := range tt.Segments {
		tt.Segments[i].ReverseRunSeconds = cloneIntPtr(tt.Segments[i].ReverseRunSeconds)
	}
	return tt
}

func cloneLine(g GeoLineString) GeoLineString {
	g.Coordinates = cloneMatrix(g.Coordinates)
	return g
}

func cloneGeoPoint(p GeoPoint) GeoPoint {
	p.Coordinates = slices.Clone(p.Coordinates)
	return p
}

func cloneMatrix(in [][]float64) [][]float64 {
	if in == nil {
		return nil
	}
	out := make([][]float64, len(in))
	for i := range in {
		out[i] = slices.Clone(in[i])
	}
	return out
}

func cloneBoardingWait(p *BoardingWaitOverride) *BoardingWaitOverride {
	if p == nil {
		return nil
	}
	c := *p
	c.Secs = cloneIntPtr(p.Secs)
	return &c
}

func cloneStringPtr(p *string) *string {
	if p == nil {
		return nil
	}
	s := *p
	return &s
}

func cloneIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	n := *p
	return &n
}
