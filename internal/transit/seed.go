package transit

import (
	"context"
	"fmt"
	"io/fs"
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

func loadEmbeddedScenarios() ([]embeddedScenario, error) {
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
