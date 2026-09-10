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
	entries, err := fs.ReadDir(dataFS, "data/scenarios")
	if err != nil {
		return fmt.Errorf("transit: reading scenarios dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := seedScenario(ctx, sink, e.Name()); err != nil {
			return fmt.Errorf("transit: seeding scenario %q: %w", e.Name(), err)
		}
	}
	return nil
}

func seedScenario(ctx context.Context, sink SeedSink, slug string) error {
	base := "data/scenarios/" + slug

	var sc Scenario
	if err := unmarshalFile(dataFS, base+"/scenario.yaml", &sc); err != nil {
		return err
	}
	if err := sink.CreateScenario(ctx, sc); err != nil {
		return fmt.Errorf("creating scenario: %w", err)
	}

	var vts []VehicleType
	if err := unmarshalFile(dataFS, base+"/vehicle_types.yaml", &vts); err != nil {
		return err
	}
	for _, vt := range vts {
		if err := sink.CreateVehicleType(ctx, vt); err != nil {
			return fmt.Errorf("creating vehicle type %q: %w", vt.ID, err)
		}
	}

	var routes []Route
	if err := unmarshalFile(dataFS, base+"/routes.yaml", &routes); err != nil {
		return err
	}
	for _, r := range routes {
		if err := sink.CreateRoute(ctx, r); err != nil {
			return fmt.Errorf("creating route %q: %w", r.ID, err)
		}
	}

	var stations []Station
	if err := unmarshalFile(dataFS, base+"/stations.yaml", &stations); err != nil {
		return err
	}
	for _, st := range stations {
		if err := sink.CreateStation(ctx, st); err != nil {
			return fmt.Errorf("creating station %q: %w", st.ID, err)
		}
	}

	var services []Service
	if err := unmarshalFile(dataFS, base+"/services.yaml", &services); err != nil {
		return err
	}
	for _, svc := range services {
		if svc.BoardingWait != nil {
			if _, err := svc.BoardingWait.Parse(); err != nil {
				return fmt.Errorf("service %q: %w", svc.ID, err)
			}
		}
		if err := sink.CreateService(ctx, svc); err != nil {
			return fmt.Errorf("creating service %q: %w", svc.ID, err)
		}
		if err := sink.AddServiceToScenario(ctx, svc.ScenarioID, svc.ID); err != nil {
			return fmt.Errorf("linking service %q to scenario: %w", svc.ID, err)
		}
	}

	var tt TravelTimes
	if err := unmarshalFile(dataFS, base+"/segment_run_times.yaml", &tt); err != nil {
		return err
	}
	if err := validateSegmentRoutes(routes, tt); err != nil {
		return err
	}
	if err := sink.UpsertTravelTimes(ctx, tt); err != nil {
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
