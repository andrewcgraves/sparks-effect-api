package transit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

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
	ListCuratedScenarios(ctx context.Context) ([]Scenario, error)
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

// CompileSeededIfNeeded compiles every seeded scenario whose stored graph does
// not match what its current rows compile to, persisting each result as a
// succeeded job, and reports how many it compiled.
//
// Seeding populates scenarios, routes, stations and services but no graph, and
// since SPA-181 the public isochrone resolves its transit data through the
// latest succeeded compile job — so without this a freshly deployed
// environment would answer "no compiled graph" until an admin triggered a
// compile by hand. Running it on every boot is safe because compiling is pure
// data work: no routing service, no queue, nothing external to be unavailable.
//
// # Why the check is a comparison and not "does a job exist"
//
// It used to skip any scenario that already had a succeeded graph, which made a
// deployed database's graph permanent: a compiled graph carries the station
// coordinates the isochrone is centred on (seededNodes), and nothing recompiled
// a seeded scenario once one existed. SPA-222 corrected the las-vegas
// coordinate in both the seed and — via migration 00015 — the deployed
// stations table, and the isochrone kept plotting Las Vegas at the old
// city-centroid geocode 13.8 km away, because the graph it reads was compiled
// before the correction. The map pin moved (it is served from the embedded
// store) and the polygon did not, so the bloom appeared detached from its
// station.
//
// That is a hazard for every seed correction, not just that one — a moved
// station, a recalibrated run time, a new service — and none of them announce
// themselves. Comparing what the rows compile to now against what is stored
// closes the class: whatever changed, the next boot notices and recompiles.
//
// # Why this is still idempotent
//
// A recompile of unchanged data is byte-identical (both underlying reads are
// deterministically ordered), so an unchanged scenario compares equal and
// writes nothing. A restart against a populated, current database therefore
// still does no work — it merely spends one in-memory compile per scenario
// proving so.
//
// A scenario that has changed gains a *new* succeeded job rather than having
// its old one rewritten or deleted. Readers take the most recent
// (latestSucceededJobBySlug), so the new graph takes over with no window in
// which the scenario has none; routing_jobs and isochrone_cache both reference
// jobs ON DELETE CASCADE, so deleting the stale job instead would drop
// in-flight routing jobs that clients are still polling. The new job id also
// falls outside every isochrone_cache key cut against the old graph, so the
// worker re-cuts the moved station's polygons without anything having to purge
// that cache.
//
// The job it writes is unowned (see Job.OwnerID). Seeding runs before any
// account exists, and needing admin credentials to get a public graph is the
// manual step this closes.
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

// sameCompiledGraph reports whether a stored graph and a freshly compiled one
// describe the same thing.
//
// Both sides are marshalled and the bytes compared, rather than compared as Go
// values, because only one of them has been through storage. jsonb holds no
// distinction between a nil slice and an empty one, so a graph that went to the
// database and back can differ from the identical graph in memory by exactly
// that — and a comparison that called such a pair different would recompile
// every scenario on every boot, forever, with the "recompiled" log line
// insisting something had changed. Encoding both sides normalises that away:
// marshalling is what the stored side was produced by, so it is the one form in
// which the two are comparable.
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

// recordCompiled records an already-compiled graph as a succeeded job.
//
// The job is created first and completed second, mirroring the async path's
// queued → succeeded transition rather than inventing a way to write a finished
// job in one shot. A write that fails between the two leaves the queued row
// behind and returns the error, which aborts the boot; only succeeded jobs are
// ever resolved, so the abandoned row means nothing to a reader.
//
// The compile itself happens in the caller, which needs the graph in hand to
// decide whether recording it is necessary at all. A compile that fails aborts
// the boot there — the same stance LoadStore already takes, since it compiles
// the same rows from the same data and would fail immediately afterwards
// regardless.
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
