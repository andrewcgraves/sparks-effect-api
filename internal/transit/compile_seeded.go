package transit

import (
	"context"
	"fmt"

	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
)

// SeededCompileSource is what compiling one seeded scenario reads: its
// composition, the global vehicle types, and the calibrated segment run times
// the graph's edge weights come from.
type SeededCompileSource interface {
	ListRoutesByScenario(ctx context.Context, scenarioID string) ([]Route, error)
	ListStationsByScenario(ctx context.Context, scenarioID string) ([]Station, error)
	ListServicesByScenario(ctx context.Context, scenarioID string) ([]Service, error)
	ListVehicleTypes(ctx context.Context) ([]VehicleType, error)
	GetTravelTimes(ctx context.Context, scenarioSlug string) (TravelTimes, bool, error)
}

// SeededCompileStore adds to that the scenario list and the job writes, so a
// boot can compile every seeded scenario and persist each result as the
// succeeded job that identifies it.
type SeededCompileStore interface {
	SeededCompileSource
	ListScenarios(ctx context.Context) ([]Scenario, error)
	GetLatestSucceededJob(ctx context.Context, scenarioSlug, kind string) (Job, bool, error)
	CreateJob(ctx context.Context, j Job) error
	CompleteJob(ctx context.Context, id string, result TransitGraph, compiledServiceIDs []string) error
}

// CompileSeededScenario compiles one seeded scenario into its TransitGraph.
//
// It compiles from the scenario's calibrated segment run times (Compile), not
// from track geometry and vehicle kinematics (CompileScenario). The seeded
// corridor's run times are authored against a published timetable and are what
// the public isochrone has always answered with; a physics profile over the
// same alignment produces materially faster, uncalibrated times, so compiling
// that way would change the public answer rather than preserve it. The physics
// compile remains the path for user-authored services, which have no such
// table to calibrate against.
//
// This is the one place a seeded scenario is compiled: the boot compile below
// and the worker's compile-job path both route through it, so a graph produced
// on startup and one produced by POST /api/scenarios/{slug}/compile are the
// same graph.
func CompileSeededScenario(ctx context.Context, src SeededCompileSource, sc Scenario) (TransitGraph, error) {
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

	graph, err := Compile(sc, routes, stations, services, vehicleTypes, tt)
	if err != nil {
		return TransitGraph{}, fmt.Errorf("transit: compiling %q: %w", sc.Slug, err)
	}
	return *graph, nil
}

// CompileSeededIfNeeded compiles every seeded scenario that has no succeeded
// compile job yet, persisting each result as one, and reports how many it
// compiled.
//
// Seeding populates scenarios, routes, stations and services but no graph, and
// since SPA-181 the public isochrone resolves its transit data through the
// latest succeeded compile job — so without this a freshly deployed
// environment would answer "no compiled graph" until an admin triggered a
// compile by hand. Running it on every boot is safe because compiling is pure
// data work: no routing service, no queue, nothing external to be unavailable.
//
// It is idempotent by the same rule SeedIfEmpty uses — a scenario that already
// has a succeeded graph is skipped — so a restart against a populated database
// does no work. That also covers a database seeded before this existed: it has
// scenarios but no job, and gains one on the next boot.
//
// The job it writes is unowned (see Job.OwnerID). Seeding runs before any
// account exists, and needing admin credentials to get a public graph is the
// manual step this closes.
func CompileSeededIfNeeded(ctx context.Context, store SeededCompileStore) (int, error) {
	scenarios, err := store.ListScenarios(ctx)
	if err != nil {
		return 0, fmt.Errorf("transit: listing scenarios to compile: %w", err)
	}

	compiled := 0
	for _, sc := range scenarios {
		_, found, err := store.GetLatestSucceededJob(ctx, sc.Slug, JobKindCompileScenario)
		if err != nil {
			return compiled, fmt.Errorf("transit: checking compiled graph for %q: %w", sc.Slug, err)
		}
		if found {
			continue
		}
		if err := compileAndRecord(ctx, store, sc); err != nil {
			return compiled, err
		}
		compiled++
	}
	return compiled, nil
}

// compileAndRecord compiles one scenario and records it as a succeeded job.
//
// The job is created first and completed second, mirroring the async path's
// queued → succeeded transition rather than inventing a way to write a finished
// job in one shot. A compile that fails leaves the queued row behind, which is
// harmless: only succeeded jobs are ever resolved, and the next boot simply
// tries again.
func compileAndRecord(ctx context.Context, store SeededCompileStore, sc Scenario) error {
	graph, err := CompileSeededScenario(ctx, store, sc)
	if err != nil {
		return err
	}

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

// CompiledServiceIDs is the set of member service ids a compiled graph
// contains, in the order the graph lists them. Every ServiceGraph is keyed by
// its source service id, so the graph is itself the record of what compiled —
// no separate bookkeeping that could drift from it.
func CompiledServiceIDs(g TransitGraph) []string {
	if len(g.Services) == 0 {
		return nil
	}
	ids := make([]string, len(g.Services))
	for i, sg := range g.Services {
		ids[i] = sg.ServiceID
	}
	return ids
}
