package transit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
)

type SeededCompileSource interface {
	ListRoutesByScenario(ctx context.Context, scenarioID string) ([]Route, error)
	ListStationsByScenario(ctx context.Context, scenarioID string) ([]Station, error)
	ListServicesByScenario(ctx context.Context, scenarioID string) ([]Service, error)
	ListVehicleTypes(ctx context.Context) ([]VehicleType, error)
	GetTravelTimes(ctx context.Context, scenarioSlug string) (TravelTimes, bool, error)
}

type SeededCompileStore interface {
	SeededCompileSource
	ListCuratedScenarios(ctx context.Context) ([]Scenario, error)
	GetLatestSucceededJob(ctx context.Context, scenarioSlug, kind string) (Job, bool, error)
	CreateJob(ctx context.Context, j Job) error
	CompleteJob(ctx context.Context, id string, result TransitGraph, compiledServiceIDs []string) error
}

func CompileSeededScenario(ctx context.Context, src SeededCompileSource, sc Scenario, boardingWait BoardingWaitPolicy) (TransitGraph, error) {
	routes, err := src.ListRoutesByScenario(ctx, sc.ID)
	if err != nil {
		return TransitGraph{}, fmt.Errorf("transit: loading routes for %q: %w", sc.Slug, err)
	}
	stations, err := src.ListStationsByScenario(ctx, sc.ID)
	if err != nil {
		return TransitGraph{}, fmt.Errorf("transit: loading stations for %q: %w", sc.Slug, err)
	}
	services, err := src.ListServicesByScenario(ctx, sc.ID)
	if err != nil {
		return TransitGraph{}, fmt.Errorf("transit: loading services for %q: %w", sc.Slug, err)
	}
	vehicleTypes, err := src.ListVehicleTypes(ctx)
	if err != nil {
		return TransitGraph{}, fmt.Errorf("transit: loading vehicle types: %w", err)
	}
	tt, found, err := src.GetTravelTimes(ctx, sc.Slug)
	if err != nil {
		return TransitGraph{}, fmt.Errorf("transit: loading travel times for %q: %w", sc.Slug, err)
	}
	if !found {
		return TransitGraph{}, fmt.Errorf("transit: scenario %q has no travel times to compile", sc.Slug)
	}

	graph, err := Compile(sc, routes, stations, services, vehicleTypes, tt, boardingWait)
	if err != nil {
		return TransitGraph{}, fmt.Errorf("transit: compiling %q: %w", sc.Slug, err)
	}
	return *graph, nil
}

func CompileSeededIfNeeded(ctx context.Context, store SeededCompileStore, boardingWait BoardingWaitPolicy) (int, error) {
	scenarios, err := store.ListCuratedScenarios(ctx)
	if err != nil {
		return 0, fmt.Errorf("transit: listing scenarios to compile: %w", err)
	}

	compiled := 0
	for _, sc := range scenarios {
		job, found, err := store.GetLatestSucceededJob(ctx, sc.Slug, JobKindCompileScenario)
		if err != nil {
			return compiled, fmt.Errorf("transit: checking compiled graph for %q: %w", sc.Slug, err)
		}

		graph, err := CompileSeededScenario(ctx, store, sc, boardingWait)
		if err != nil {
			return compiled, err
		}

		if found && job.Result != nil {
			same, err := sameCompiledGraph(*job.Result, graph)
			if err != nil {
				return compiled, fmt.Errorf("transit: comparing compiled graph for %q: %w", sc.Slug, err)
			}
			if same {
				slog.Debug("transit: scenario already compiled and current, skipping",
					"scenario_slug", sc.Slug, "compile_job_id", job.ID)
				continue
			}
			slog.Info("transit: stored graph no longer matches its source data, recompiling",
				"scenario_slug", sc.Slug, "superseded_compile_job_id", job.ID)
		}

		if err := recordCompiled(ctx, store, sc, graph); err != nil {
			return compiled, err
		}
		compiled++
	}
	return compiled, nil
}

func sameCompiledGraph(stored, fresh TransitGraph) (bool, error) {
	a, err := json.Marshal(stored)
	if err != nil {
		return false, fmt.Errorf("marshalling stored graph: %w", err)
	}
	b, err := json.Marshal(fresh)
	if err != nil {
		return false, fmt.Errorf("marshalling compiled graph: %w", err)
	}
	return bytes.Equal(a, b), nil
}

func recordCompiled(ctx context.Context, store SeededCompileStore, sc Scenario, graph TransitGraph) error {
	id, err := ids.NewUUID()
	if err != nil {
		return err
	}
	scenarioID := sc.ID
	job := Job{
		ID:         id,
		Kind:       JobKindCompileScenario,
		Status:     JobStatusQueued,
		ScenarioID: &scenarioID,
	}
	if err := store.CreateJob(ctx, job); err != nil {
		return fmt.Errorf("transit: creating compile job for %q: %w", sc.Slug, err)
	}
	if err := store.CompleteJob(ctx, id, graph, CompiledServiceIDs(graph)); err != nil {
		return fmt.Errorf("transit: recording compiled graph for %q: %w", sc.Slug, err)
	}
	return nil
}
