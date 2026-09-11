package transit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

type SeedReconciler interface {
	SeedSink
	GetScenarioBySlug(ctx context.Context, slug string) (Scenario, bool, error)
	ListVehicleTypes(ctx context.Context) ([]VehicleType, error)
	ListRoutesByScenario(ctx context.Context, scenarioID string) ([]Route, error)
	ListStationsByScenario(ctx context.Context, scenarioID string) ([]Station, error)
	ListServicesByScenario(ctx context.Context, scenarioID string) ([]Service, error)
	GetTravelTimes(ctx context.Context, scenarioSlug string) (TravelTimes, bool, error)
	UpdateScenario(ctx context.Context, sc Scenario) error
	UpdateVehicleType(ctx context.Context, vt VehicleType) error
	UpdateRoute(ctx context.Context, r Route) error
	UpdateStation(ctx context.Context, st Station) error
	UpdateService(ctx context.Context, svc Service) error
}

func ReconcileSeed(ctx context.Context, store SeedReconciler) (int, error) {
	seeds, err := loadEmbeddedScenarios()
	if err != nil {
		return 0, err
	}

	written := 0
	for _, seed := range seeds {
		n, err := reconcileEmbeddedScenario(ctx, store, seed)
		if err != nil {
			return written, fmt.Errorf("transit: reconciling scenario %q: %w", seed.scenario.Slug, err)
		}
		written += n
	}
	return written, nil
}

func reconcileEmbeddedScenario(ctx context.Context, store SeedReconciler, seed embeddedScenario) (int, error) {
	stored, found, err := store.GetScenarioBySlug(ctx, seed.scenario.Slug)
	if err != nil {
		return 0, fmt.Errorf("looking up scenario: %w", err)
	}
	if !found {
		if err := writeEmbeddedScenario(ctx, store, seed); err != nil {
			return 0, err
		}
		n := 1 + len(seed.vehicleTypes) + len(seed.routes) + len(seed.stations) + len(seed.services) + 1
		slog.Info("transit: seeded embedded scenario into an empty slot",
			"scenario_slug", seed.scenario.Slug, "rows", n)
		return n, nil
	}
	if stored.OwnerID != nil {
		slog.Warn("transit: skipping seed reconciliation for an authored scenario with a seeded slug",
			"scenario_slug", seed.scenario.Slug, "scenario_id", stored.ID)
		return 0, nil
	}
	if stored.ID != seed.scenario.ID {
		slog.Warn("transit: skipping seed reconciliation; curated scenario slug matches but id does not",
			"scenario_slug", seed.scenario.Slug, "stored_id", stored.ID, "seed_id", seed.scenario.ID)
		return 0, nil
	}

	written := 0
	same, err := sameSeedContent(scenarioSeedView(stored), scenarioSeedView(seed.scenario))
	if err != nil {
		return 0, err
	}
	if !same {
		if err := store.UpdateScenario(ctx, seed.scenario); err != nil {
			return written, fmt.Errorf("updating scenario: %w", err)
		}
		written++
		slog.Info("transit: reconciled seeded scenario", "scenario_slug", seed.scenario.Slug)
	}

	n, err := reconcileVehicleTypes(ctx, store, seed.vehicleTypes)
	if err != nil {
		return written, err
	}
	written += n

	storedRoutes, err := store.ListRoutesByScenario(ctx, stored.ID)
	if err != nil {
		return written, fmt.Errorf("listing routes: %w", err)
	}
	n, err = reconcileRoutes(ctx, store, seed.routes, storedRoutes)
	if err != nil {
		return written, err
	}
	written += n

	storedStations, err := store.ListStationsByScenario(ctx, stored.ID)
	if err != nil {
		return written, fmt.Errorf("listing stations: %w", err)
	}
	n, err = reconcileStations(ctx, store, seed.stations, storedStations)
	if err != nil {
		return written, err
	}
	written += n

	storedServices, err := store.ListServicesByScenario(ctx, stored.ID)
	if err != nil {
		return written, fmt.Errorf("listing services: %w", err)
	}
	n, err = reconcileServices(ctx, store, seed.services, storedServices)
	if err != nil {
		return written, err
	}
	written += n

	storedTT, found, err := store.GetTravelTimes(ctx, seed.travelTimes.ScenarioSlug)
	if err != nil {
		return written, fmt.Errorf("loading travel times: %w", err)
	}
	if !found {
		if err := store.UpsertTravelTimes(ctx, seed.travelTimes); err != nil {
			return written, fmt.Errorf("upserting travel times: %w", err)
		}
		written++
		return written, nil
	}
	same, err = sameSeedContent(travelTimesSeedView(storedTT), travelTimesSeedView(seed.travelTimes))
	if err != nil {
		return written, err
	}
	if !same {
		if err := store.UpsertTravelTimes(ctx, seed.travelTimes); err != nil {
			return written, fmt.Errorf("upserting travel times: %w", err)
		}
		written++
		slog.Info("transit: reconciled seeded travel times", "scenario_slug", seed.scenario.Slug)
	}
	return written, nil
}

func reconcileVehicleTypes(ctx context.Context, store SeedReconciler, seeds []VehicleType) (int, error) {
	stored, err := store.ListVehicleTypes(ctx)
	if err != nil {
		return 0, fmt.Errorf("listing vehicle types: %w", err)
	}
	byID := make(map[string]VehicleType, len(stored))
	for _, vt := range stored {
		byID[vt.ID] = vt
	}

	written := 0
	for _, want := range seeds {
		got, ok := byID[want.ID]
		if !ok {
			if err := store.CreateVehicleType(ctx, want); err != nil {
				return written, fmt.Errorf("creating vehicle type %q: %w", want.ID, err)
			}
			written++
			continue
		}
		same, err := sameSeedContent(got, want)
		if err != nil {
			return written, err
		}
		if same {
			continue
		}
		if err := store.UpdateVehicleType(ctx, want); err != nil {
			return written, fmt.Errorf("updating vehicle type %q: %w", want.ID, err)
		}
		written++
		slog.Info("transit: reconciled seeded vehicle type", "vehicle_type_id", want.ID)
	}
	return written, nil
}

func reconcileRoutes(ctx context.Context, store SeedReconciler, seeds, stored []Route) (int, error) {
	byID := make(map[string]Route, len(stored))
	for _, r := range stored {
		byID[r.ID] = r
	}

	written := 0
	for _, want := range seeds {
		got, ok := byID[want.ID]
		if !ok {
			if err := store.CreateRoute(ctx, want); err != nil {
				return written, fmt.Errorf("creating route %q: %w", want.ID, err)
			}
			written++
			continue
		}
		if got.OwnerID != nil {
			continue
		}
		same, err := sameSeedContent(routeSeedView(got), routeSeedView(want))
		if err != nil {
			return written, err
		}
		if same {
			continue
		}
		if err := store.UpdateRoute(ctx, want); err != nil {
			return written, fmt.Errorf("updating route %q: %w", want.ID, err)
		}
		written++
		slog.Info("transit: reconciled seeded route", "route_id", want.ID, "route_slug", want.Slug)
	}
	return written, nil
}

func reconcileStations(ctx context.Context, store SeedReconciler, seeds, stored []Station) (int, error) {
	byID := make(map[string]Station, len(stored))
	for _, st := range stored {
		byID[st.ID] = st
	}

	written := 0
	for _, want := range seeds {
		got, ok := byID[want.ID]
		if !ok {
			if err := store.CreateStation(ctx, want); err != nil {
				return written, fmt.Errorf("creating station %q: %w", want.ID, err)
			}
			written++
			continue
		}
		if got.OwnerID != nil {
			continue
		}
		same, err := sameSeedContent(stationSeedView(got), stationSeedView(want))
		if err != nil {
			return written, err
		}
		if same {
			continue
		}
		if err := store.UpdateStation(ctx, want); err != nil {
			return written, fmt.Errorf("updating station %q: %w", want.ID, err)
		}
		written++
		slog.Info("transit: reconciled seeded station", "station_id", want.ID, "station_slug", want.Slug)
	}
	return written, nil
}

func reconcileServices(ctx context.Context, store SeedReconciler, seeds, stored []Service) (int, error) {
	byID := make(map[string]Service, len(stored))
	for _, svc := range stored {
		byID[svc.ID] = svc
	}

	written := 0
	for _, want := range seeds {
		got, ok := byID[want.ID]
		if !ok {
			if err := store.CreateService(ctx, want); err != nil {
				return written, fmt.Errorf("creating service %q: %w", want.ID, err)
			}
			if err := store.AddServiceToScenario(ctx, want.ScenarioID, want.ID); err != nil {
				return written, fmt.Errorf("linking service %q to scenario: %w", want.ID, err)
			}
			written++
			continue
		}
		if got.OwnerID != nil {
			continue
		}
		same, err := sameSeedContent(serviceSeedView(got), serviceSeedView(want))
		if err != nil {
			return written, err
		}
		if same {
			continue
		}
		if err := store.UpdateService(ctx, want); err != nil {
			return written, fmt.Errorf("updating service %q: %w", want.ID, err)
		}
		if err := store.AddServiceToScenario(ctx, want.ScenarioID, want.ID); err != nil {
			return written, fmt.Errorf("linking service %q to scenario: %w", want.ID, err)
		}
		written++
		slog.Info("transit: reconciled seeded service", "service_id", want.ID)
	}
	return written, nil
}

func sameSeedContent(a, b any) (bool, error) {
	left, err := json.Marshal(a)
	if err != nil {
		return false, fmt.Errorf("marshalling stored seed row: %w", err)
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false, fmt.Errorf("marshalling embedded seed row: %w", err)
	}
	return bytes.Equal(left, right), nil
}

func scenarioSeedView(sc Scenario) Scenario {
	sc.OwnerID = nil
	return sc
}

func stationSeedView(st Station) Station {
	st.OwnerID = nil
	return st
}

func routeSeedView(r Route) Route {
	r.OwnerID = nil
	if r.Segments == nil {
		r.Segments = []RouteSegment{}
	}
	return r
}

func serviceSeedView(svc Service) Service {
	svc.OwnerID = nil
	svc.BoardingWaitPolicy = ""
	svc.BoardingWaitSecs = 0
	svc.BoardingWaitSource = ""
	if svc.Stops == nil {
		svc.Stops = []ServiceStop{}
	}
	if svc.FrequencyWindows == nil {
		svc.FrequencyWindows = []FrequencyWindow{}
	}
	return svc
}

func travelTimesSeedView(tt TravelTimes) TravelTimes {
	if tt.Segments == nil {
		tt.Segments = []SegmentTime{}
	}
	return tt
}
