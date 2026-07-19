package transit

import (
	"context"
	"fmt"
	"io/fs"
)

// SeedIfEmpty loads the embedded scenario seed data through the repository, but
// only when the store has no scenarios yet. It is safe to call on every boot:
// the first run against an empty database populates it; subsequent runs are
// no-ops. This is the "seed lands on first migration" path.
func SeedIfEmpty(ctx context.Context, repo Repository) (bool, error) {
	existing, err := repo.ListScenarios(ctx)
	if err != nil {
		return false, fmt.Errorf("transit: checking for existing scenarios: %w", err)
	}
	if len(existing) > 0 {
		return false, nil
	}
	if err := SeedFromEmbedded(ctx, repo); err != nil {
		return false, err
	}
	return true, nil
}

// SeedFromEmbedded parses every embedded scenario's YAML and writes it through
// the repository, in dependency order (scenario → vehicle types → routes →
// stations → services → travel times → curated membership). It does not check
// for existing rows; callers wanting idempotency should use SeedIfEmpty.
func SeedFromEmbedded(ctx context.Context, repo Repository) error {
	entries, err := fs.ReadDir(dataFS, "data/scenarios")
	if err != nil {
		return fmt.Errorf("transit: reading scenarios dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := seedScenario(ctx, repo, e.Name()); err != nil {
			return fmt.Errorf("transit: seeding scenario %q: %w", e.Name(), err)
		}
	}
	return nil
}

func seedScenario(ctx context.Context, repo Repository, slug string) error {
	base := "data/scenarios/" + slug

	var sc Scenario
	if err := unmarshalFile(dataFS, base+"/scenario.yaml", &sc); err != nil {
		return err
	}
	if err := repo.CreateScenario(ctx, sc); err != nil {
		return fmt.Errorf("creating scenario: %w", err)
	}

	var vts []VehicleType
	if err := unmarshalFile(dataFS, base+"/vehicle_types.yaml", &vts); err != nil {
		return err
	}
	for _, vt := range vts {
		if err := repo.CreateVehicleType(ctx, vt); err != nil {
			return fmt.Errorf("creating vehicle type %q: %w", vt.ID, err)
		}
	}

	var routes []Route
	if err := unmarshalFile(dataFS, base+"/routes.yaml", &routes); err != nil {
		return err
	}
	for _, r := range routes {
		if err := repo.CreateRoute(ctx, r); err != nil {
			return fmt.Errorf("creating route %q: %w", r.ID, err)
		}
	}

	var stations []Station
	if err := unmarshalFile(dataFS, base+"/stations.yaml", &stations); err != nil {
		return err
	}
	for _, st := range stations {
		if err := repo.CreateStation(ctx, st); err != nil {
			return fmt.Errorf("creating station %q: %w", st.ID, err)
		}
	}

	var services []Service
	if err := unmarshalFile(dataFS, base+"/services.yaml", &services); err != nil {
		return err
	}
	for _, svc := range services {
		if err := repo.CreateService(ctx, svc); err != nil {
			return fmt.Errorf("creating service %q: %w", svc.ID, err)
		}
		if err := repo.AddServiceToScenario(ctx, svc.ScenarioID, svc.ID); err != nil {
			return fmt.Errorf("linking service %q to scenario: %w", svc.ID, err)
		}
	}

	var tt TravelTimes
	if err := unmarshalFile(dataFS, base+"/segment_run_times.yaml", &tt); err != nil {
		return err
	}
	if err := repo.UpsertTravelTimes(ctx, tt); err != nil {
		return fmt.Errorf("upserting travel times: %w", err)
	}

	return nil
}
