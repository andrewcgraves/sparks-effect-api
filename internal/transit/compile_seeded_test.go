package transit

import (
	"context"
	"testing"
)

// seededCompileFake is a SeededCompileStore backed by the embedded seed data,
// so the boot compile can be driven end to end — read rows, compile, persist a
// succeeded job — with no database.
type seededCompileFake struct {
	store *Store
	jobs  []Job
	// createdJobs counts CreateJob calls, so a test can tell "compiled again"
	// from "left alone".
	createdJobs int
}

func newSeededCompileFake(t *testing.T) *seededCompileFake {
	t.Helper()
	return &seededCompileFake{store: mustNewStore(t)}
}

func (f *seededCompileFake) ListScenarios(context.Context) ([]Scenario, error) {
	return f.store.GetScenarios(), nil
}

func (f *seededCompileFake) ListRoutesByScenario(_ context.Context, scenarioID string) ([]Route, error) {
	return f.store.GetRoutesByScenario(scenarioID), nil
}

func (f *seededCompileFake) ListStationsByScenario(_ context.Context, scenarioID string) ([]Station, error) {
	return f.store.GetStationsByScenario(scenarioID), nil
}

func (f *seededCompileFake) ListServicesByScenario(_ context.Context, scenarioID string) ([]Service, error) {
	return f.store.GetServicesByScenario(scenarioID), nil
}

func (f *seededCompileFake) ListVehicleTypes(context.Context) ([]VehicleType, error) {
	return f.store.vehicleTypes, nil
}

func (f *seededCompileFake) GetTravelTimes(_ context.Context, scenarioSlug string) (TravelTimes, bool, error) {
	tt, ok := f.store.GetTravelTimes(scenarioSlug)
	return tt, ok, nil
}

func (f *seededCompileFake) GetLatestSucceededJob(_ context.Context, scenarioSlug, kind string) (Job, bool, error) {
	sc, ok := f.store.GetScenarioBySlug(scenarioSlug)
	if !ok {
		return Job{}, false, nil
	}
	for i := len(f.jobs) - 1; i >= 0; i-- {
		j := f.jobs[i]
		if j.Kind == kind && j.Status == JobStatusSucceeded && j.ScenarioID != nil && *j.ScenarioID == sc.ID {
			return j, true, nil
		}
	}
	return Job{}, false, nil
}

func (f *seededCompileFake) CreateJob(_ context.Context, j Job) error {
	f.jobs = append(f.jobs, j)
	f.createdJobs++
	return nil
}

func (f *seededCompileFake) CompleteJob(_ context.Context, id string, result TransitGraph, compiledServiceIDs []string) error {
	for i := range f.jobs {
		if f.jobs[i].ID == id {
			f.jobs[i].Status = JobStatusSucceeded
			f.jobs[i].Result = &result
			f.jobs[i].CompiledServiceIDs = compiledServiceIDs
			return nil
		}
	}
	return nil
}

// graphFor returns the graph the fake's latest succeeded job holds for a slug.
func (f *seededCompileFake) graphFor(t *testing.T, slug string) *TransitGraph {
	t.Helper()
	job, found, err := f.GetLatestSucceededJob(context.Background(), slug, JobKindCompileScenario)
	if err != nil {
		t.Fatalf("GetLatestSucceededJob: %v", err)
	}
	if !found {
		t.Fatalf("no succeeded compile job for scenario %q", slug)
	}
	if job.Result == nil {
		t.Fatalf("succeeded job for %q has no result", slug)
	}
	return job.Result
}

// The seeded isochrone reads its transit data from the compile job's graph
// rather than the embedded store, so the two must answer identically or the
// swap silently changes what the public endpoint returns (SPA-181).
//
// This is a pure data comparison over both IsochroneData implementations — no
// routing calls, no chainer.
func TestCompileSeededIfNeeded_graphMatchesEmbeddedStore(t *testing.T) {
	ctx := context.Background()
	fake := newSeededCompileFake(t)

	if _, err := CompileSeededIfNeeded(ctx, fake); err != nil {
		t.Fatalf("CompileSeededIfNeeded: %v", err)
	}

	for _, sc := range fake.store.GetScenarios() {
		var seeded IsochroneData = fake.store
		var compiled IsochroneData = CompiledGraphData{Graph: fake.graphFor(t, sc.Slug)}

		seededNodes, ok := seeded.Nodes(sc.Slug)
		if !ok {
			t.Fatalf("%s: embedded store has no nodes", sc.Slug)
		}
		compiledNodes, ok := compiled.Nodes(sc.Slug)
		if !ok {
			t.Fatalf("%s: compiled graph has no nodes", sc.Slug)
		}
		if len(seededNodes) == 0 {
			t.Fatalf("%s: embedded store returned an empty node set; nothing is being compared", sc.Slug)
		}

		bySlug := make(map[string]Node, len(compiledNodes))
		for _, n := range compiledNodes {
			bySlug[n.Slug] = n
		}
		if len(bySlug) != len(seededNodes) {
			t.Fatalf("%s: compiled node set has %d distinct slugs, embedded store has %d",
				sc.Slug, len(bySlug), len(seededNodes))
		}
		for _, want := range seededNodes {
			got, found := bySlug[want.Slug]
			if !found {
				t.Fatalf("%s: compiled graph is missing node %q", sc.Slug, want.Slug)
			}
			if got != want {
				t.Errorf("%s: node %q = %+v, want %+v", sc.Slug, want.Slug, got, want)
			}
		}

		// Every ordered station pair, including a node to itself.
		for _, from := range seededNodes {
			for _, to := range seededNodes {
				wSecs, wWait, wSvc, wOK := seeded.TravelTimeBetween(sc.Slug, from.Slug, to.Slug)
				gSecs, gWait, gSvc, gOK := compiled.TravelTimeBetween(sc.Slug, from.Slug, to.Slug)
				if wOK != gOK || wSecs != gSecs || wWait != gWait || wSvc != gSvc {
					t.Errorf("%s: %s→%s: compiled = (%d, %d, %q, %v), embedded = (%d, %d, %q, %v)",
						sc.Slug, from.Slug, to.Slug, gSecs, gWait, gSvc, gOK, wSecs, wWait, wSvc, wOK)
				}
			}
		}
	}
}

// Compiling at boot must be idempotent: a database that already carries a
// compiled graph is left alone, so a restart is not a recompile.
func TestCompileSeededIfNeeded_skipsAlreadyCompiledScenarios(t *testing.T) {
	ctx := context.Background()
	fake := newSeededCompileFake(t)

	compiled, err := CompileSeededIfNeeded(ctx, fake)
	if err != nil {
		t.Fatalf("CompileSeededIfNeeded: %v", err)
	}
	wantScenarios := len(fake.store.GetScenarios())
	if compiled != wantScenarios {
		t.Fatalf("first run compiled %d scenarios, want %d", compiled, wantScenarios)
	}

	again, err := CompileSeededIfNeeded(ctx, fake)
	if err != nil {
		t.Fatalf("CompileSeededIfNeeded (second): %v", err)
	}
	if again != 0 {
		t.Errorf("second run compiled %d scenarios, want 0", again)
	}
	if fake.createdJobs != wantScenarios {
		t.Errorf("jobs created = %d, want %d (one per scenario, never re-created)",
			fake.createdJobs, wantScenarios)
	}
}

// The job a boot compile writes carries no owner: seeding happens before any
// account exists, and requiring admin credentials to get a public graph is the
// manual step this closes.
func TestCompileSeededIfNeeded_jobIsUnowned(t *testing.T) {
	ctx := context.Background()
	fake := newSeededCompileFake(t)

	if _, err := CompileSeededIfNeeded(ctx, fake); err != nil {
		t.Fatalf("CompileSeededIfNeeded: %v", err)
	}

	for _, j := range fake.jobs {
		if j.OwnerID != nil {
			t.Errorf("job %s has owner %q, want none", j.ID, *j.OwnerID)
		}
		if j.Kind != JobKindCompileScenario {
			t.Errorf("job %s kind = %q, want %q", j.ID, j.Kind, JobKindCompileScenario)
		}
		if j.ScenarioID == nil {
			t.Errorf("job %s has no scenario target", j.ID)
		}
		if len(j.CompiledServiceIDs) == 0 {
			t.Errorf("job %s recorded no compiled service ids", j.ID)
		}
	}
}
