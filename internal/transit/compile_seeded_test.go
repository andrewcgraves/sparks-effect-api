package transit

import (
	"context"
	"encoding/json"
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
	// movedStations overrides a station's coordinates by slug, standing in for
	// the correcting UPDATE a migration runs against a deployed database (see
	// moveStation).
	movedStations map[string]GeoPoint
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
	stations := f.store.GetStationsByScenario(scenarioID)
	out := make([]Station, len(stations))
	for i, st := range stations {
		if loc, moved := f.movedStations[st.Slug]; moved {
			st.Location = loc
		}
		out[i] = st
	}
	return out, nil
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

// CompleteJob stores the result through a JSON round trip rather than keeping
// the caller's value, because jobs.result is a jsonb column and that round trip
// is the one thing a stored graph has been through that a freshly compiled one
// has not. CompileSeededIfNeeded now compares the two to decide whether to
// recompile, so a fake that skipped the encoding would be unable to catch the
// failure that comparison most plausibly has: reporting a graph changed because
// of how storage represents it, and recompiling on every boot forever.
func (f *seededCompileFake) CompleteJob(_ context.Context, id string, result TransitGraph, compiledServiceIDs []string) error {
	stored, err := roundTripGraph(result)
	if err != nil {
		return err
	}
	for i := range f.jobs {
		if f.jobs[i].ID == id {
			f.jobs[i].Status = JobStatusSucceeded
			f.jobs[i].Result = stored
			f.jobs[i].CompiledServiceIDs = compiledServiceIDs
			return nil
		}
	}
	return nil
}

// roundTripGraph encodes and decodes a graph the way jobs.result does.
func roundTripGraph(g TransitGraph) (*TransitGraph, error) {
	b, err := json.Marshal(g)
	if err != nil {
		return nil, err
	}
	var out TransitGraph
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// moveStation repositions a station in the rows this fake reads, without
// touching any graph already compiled from them — the state a deployed database
// is left in by a migration that corrects a coordinate (SPA-222's 00015).
func (f *seededCompileFake) moveStation(t *testing.T, slug string, lng, lat float64) {
	t.Helper()
	if f.movedStations == nil {
		f.movedStations = make(map[string]GeoPoint)
	}
	f.movedStations[slug] = GeoPoint{Type: "Point", Coordinates: []float64{lng, lat}}
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

	if _, err := CompileSeededIfNeeded(ctx, fake, DefaultBoardingWaitPolicy()); err != nil {
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

// A corrected station coordinate must reach the isochrone (SPA-222 follow-up).
//
// The graph carries the coordinates the isochrone is centred on, and it used to
// be compiled once and never again — so correcting a station in the seed and in
// the deployed rows left the polygon being cut around the old position
// indefinitely, while the map pin (served from the embedded store) moved. This
// is that failure in miniature, in the order it actually happened: seed the
// pre-correction coordinate, compile, apply the correction to the rows, and
// require the next boot to carry it into the graph.
func TestCompileSeededIfNeeded_recompilesAfterAStationMoves(t *testing.T) {
	ctx := context.Background()
	fake := newSeededCompileFake(t)

	// The database as it stood before SPA-222: the city-centroid geocode.
	fake.moveStation(t, "las-vegas", -115.136, 36.174)
	if _, err := CompileSeededIfNeeded(ctx, fake, DefaultBoardingWaitPolicy()); err != nil {
		t.Fatalf("CompileSeededIfNeeded: %v", err)
	}

	// Migration 00015 corrects the station row under the stored graph.
	const slug = "ca-hsr"
	moved := Node{Slug: "las-vegas", Lat: 36.0545, Lng: -115.1778}
	fake.moveStation(t, moved.Slug, moved.Lng, moved.Lat)

	compiled, err := CompileSeededIfNeeded(ctx, fake, DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("CompileSeededIfNeeded after move: %v", err)
	}
	if compiled != 1 {
		t.Fatalf("compiled %d scenarios after moving a station, want 1", compiled)
	}

	nodes, ok := CompiledGraphData{Graph: fake.graphFor(t, slug)}.Nodes(slug)
	if !ok {
		t.Fatal("recompiled graph has no nodes")
	}
	for _, n := range nodes {
		if n.Slug != moved.Slug {
			continue
		}
		if n != moved {
			t.Errorf("node %q = %+v, want %+v — the graph the isochrone plots from "+
				"still holds the pre-correction position", moved.Slug, n, moved)
		}
		return
	}
	t.Fatalf("no %q node in the recompiled graph", moved.Slug)
}

// Compiling at boot must be idempotent: a database that already carries a
// compiled graph is left alone, so a restart is not a recompile.
//
// Since the skip is now a comparison against what the rows compile to rather
// than a bare "a job exists", this is also what holds the comparison itself
// honest: a graph that reports itself changed when nothing changed would
// recompile on every boot forever, and the createdJobs count below is what
// catches that.
func TestCompileSeededIfNeeded_skipsAlreadyCompiledScenarios(t *testing.T) {
	ctx := context.Background()
	fake := newSeededCompileFake(t)

	compiled, err := CompileSeededIfNeeded(ctx, fake, DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("CompileSeededIfNeeded: %v", err)
	}
	wantScenarios := len(fake.store.GetScenarios())
	if compiled != wantScenarios {
		t.Fatalf("first run compiled %d scenarios, want %d", compiled, wantScenarios)
	}

	again, err := CompileSeededIfNeeded(ctx, fake, DefaultBoardingWaitPolicy())
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

// A global boarding-wait policy change is invisible to membership/updated_at,
// but it changes WaitSecs / WaitPolicy on every ServiceGraph, so the stored
// graph must compare unequal and a new compile job must be written (SPA-236).
func TestCompileSeededIfNeeded_recompilesAfterBoardingWaitPolicyChange(t *testing.T) {
	ctx := context.Background()
	fake := newSeededCompileFake(t)

	if _, err := CompileSeededIfNeeded(ctx, fake, DefaultBoardingWaitPolicy()); err != nil {
		t.Fatalf("CompileSeededIfNeeded: %v", err)
	}
	jobsBefore := fake.createdJobs

	compiled, err := CompileSeededIfNeeded(ctx, fake, BoardingWaitPolicy{Kind: BoardingWaitHalfHeadway})
	if err != nil {
		t.Fatalf("CompileSeededIfNeeded after policy change: %v", err)
	}
	if compiled != 1 {
		t.Fatalf("compiled %d scenarios after policy change, want 1", compiled)
	}
	if fake.createdJobs != jobsBefore+1 {
		t.Errorf("jobs created = %d, want %d", fake.createdJobs, jobsBefore+1)
	}

	g := fake.graphFor(t, "ca-hsr")
	byID := map[string]ServiceGraph{}
	for _, sg := range g.Services {
		byID[sg.ServiceID] = sg
	}
	if got := byID["00000000-0000-4004-8001-000000000002"].WaitSecs; got != 1800 {
		t.Errorf("HSR Local WaitSecs after half_headway recompile: want 1800, got %d", got)
	}
}

// The job a boot compile writes carries no owner: seeding happens before any
// account exists, and requiring admin credentials to get a public graph is the
// manual step this closes.
func TestCompileSeededIfNeeded_jobIsUnowned(t *testing.T) {
	ctx := context.Background()
	fake := newSeededCompileFake(t)

	if _, err := CompileSeededIfNeeded(ctx, fake, DefaultBoardingWaitPolicy()); err != nil {
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
