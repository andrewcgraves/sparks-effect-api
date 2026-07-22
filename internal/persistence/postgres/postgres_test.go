package postgres_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// ptr returns the address of v, for the optional (pointer) fields on the
// domain types — scenario ids on routes, owner ids on services.
func ptr[T any](v T) *T { return &v }

// testDBURL resolves the throwaway Postgres connection string. It reads
// TEST_DATABASE_URL (falling back to DATABASE_URL). When neither is set the
// integration tests skip locally so `go test ./...` stays green without a DB —
// but in CI (where the CI env var is present) a missing URL is a hard failure,
// so a misconfigured pipeline can never pass green by silently skipping.
func testDBURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_DATABASE_URL (or DATABASE_URL) must be set for integration tests in CI")
		}
		t.Skip("set TEST_DATABASE_URL to run Postgres integration tests (see `make db-up`)")
	}
	return url
}

// freshRepo returns a Repo against a freshly-reset, freshly-migrated database.
// It drops and recreates the public schema so every test starts from an empty
// database — which also exercises the "migrations run cleanly from empty" path.
func freshRepo(t *testing.T) (*postgres.Repo, string) {
	t.Helper()
	url := testDBURL(t)
	ctx := context.Background()
	resetSchema(t, url)
	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo, err := postgres.Connect(ctx, url, 0)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(repo.Close)
	return repo, url
}

func resetSchema(t *testing.T, url string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("reset connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
}

func TestMigrationsRunCleanlyOnEmptyDB(t *testing.T) {
	repo, _ := freshRepo(t)
	scenarios, err := repo.ListScenarios(context.Background())
	if err != nil {
		t.Fatalf("ListScenarios: %v", err)
	}
	if len(scenarios) != 0 {
		t.Fatalf("expected empty database after migrate, got %d scenarios", len(scenarios))
	}
}

func TestSeedAndCompiledReadPathAcrossRestart(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)

	// First boot seeds; a second boot against a populated DB is a no-op.
	seeded, err := transit.SeedIfEmpty(ctx, repo)
	if err != nil {
		t.Fatalf("SeedIfEmpty: %v", err)
	}
	if !seeded {
		t.Fatal("expected seed to run on empty database")
	}
	again, err := transit.SeedIfEmpty(ctx, repo)
	if err != nil {
		t.Fatalf("SeedIfEmpty (second): %v", err)
	}
	if again {
		t.Fatal("expected second SeedIfEmpty to be a no-op")
	}

	// Simulate a process restart: an independent pool, no re-migrate/re-seed.
	repo2, err := postgres.Connect(ctx, url, 0)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer repo2.Close()

	store, err := transit.LoadStore(ctx, repo2)
	if err != nil {
		t.Fatalf("LoadStore: %v", err)
	}

	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr scenario not found after restart")
	}
	if got := len(store.GetStationsByScenario(sc.ID)); got != 13 {
		t.Errorf("stations: want 13 Phase 1 stations, got %d", got)
	}
	if got := len(store.GetServicesByScenario(sc.ID)); got != 2 {
		t.Errorf("active services: want 2 (Express + Local), got %d", got)
	}

	// The compiled-graph read path must still produce isochrone travel times
	// from the stored rows: sf→millbrae = 760 run + 90 dwell = 850.
	secs, _, svcID, ok := store.TravelTimeBetween("ca-hsr", "sf", "millbrae")
	if !ok {
		t.Fatal("TravelTimeBetween sf→millbrae not found")
	}
	if secs != 850 {
		t.Errorf("sf→millbrae: want 850s, got %d", secs)
	}
	if svcID == "" {
		t.Error("sf→millbrae: serviceID must be non-empty")
	}

	// Curated membership join was populated for the scenario.
	ids, err := repo2.ListServiceIDsByScenario(ctx, sc.ID)
	if err != nil {
		t.Fatalf("ListServiceIDsByScenario: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("scenario_service membership: want 2, got %d", len(ids))
	}
}

func TestUsersRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	u := transit.User{
		ID:      "00000000-0000-4009-8001-000000000001",
		Email:   "andrew@example.com",
		Name:    "Andrew",
		IsAdmin: true,
	}
	if err := repo.CreateUser(ctx, u, "hash-placeholder"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, ok, err := repo.GetUserByID(ctx, u.ID)
	if err != nil || !ok {
		t.Fatalf("GetUserByID: ok=%v err=%v", ok, err)
	}
	if got.Email != u.Email || !got.IsAdmin {
		t.Errorf("GetUserByID: got %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("GetUserByID: created_at not populated")
	}

	byEmail, ok, err := repo.GetUserByEmail(ctx, u.Email)
	if err != nil || !ok {
		t.Fatalf("GetUserByEmail: ok=%v err=%v", ok, err)
	}
	if byEmail.ID != u.ID {
		t.Errorf("GetUserByEmail: want id %s, got %s", u.ID, byEmail.ID)
	}

	users, err := repo.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("ListUsers: want 1, got %d", len(users))
	}
}

func TestJobsRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	j := transit.Job{
		ID:     "00000000-0000-400a-8001-000000000001",
		Kind:   "compile",
		Status: transit.JobStatusQueued,
	}
	if err := repo.CreateJob(ctx, j); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if err := repo.UpdateJobStatus(ctx, j.ID, transit.JobStatusFailed, "boom"); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}

	got, ok, err := repo.GetJobByID(ctx, j.ID)
	if err != nil || !ok {
		t.Fatalf("GetJobByID: ok=%v err=%v", ok, err)
	}
	if got.Status != transit.JobStatusFailed || got.Error != "boom" {
		t.Errorf("GetJobByID: want failed/boom, got %s/%q", got.Status, got.Error)
	}
	if got.UpdatedAt.Before(got.CreatedAt) {
		t.Error("GetJobByID: updated_at should be >= created_at")
	}

	if err := repo.UpdateJobStatus(ctx, "00000000-0000-400a-8001-0000000000ff", "x", ""); err == nil {
		t.Error("UpdateJobStatus on missing job: want error, got nil")
	}

	jobs, err := repo.ListJobs(ctx)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Errorf("ListJobs: want 1, got %d", len(jobs))
	}
	if jobs[0].Result != nil {
		t.Errorf("ListJobs: a failed job must not carry a result, got %+v", jobs[0].Result)
	}
}

// TestJobCompletionStoresResultRetrievableBySlug pins the async job model's
// headline behaviour (SPA-82): a compile job's result survives as jsonb and
// can be found by the scenario's slug alone, with no job id in hand.
func TestJobCompletionStoresResultRetrievableBySlug(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	sc := transit.Scenario{ID: "00000000-0000-4001-8001-000000000009", Slug: "graph-scenario", Name: "Graph Scenario"}
	if err := repo.CreateScenario(ctx, sc); err != nil {
		t.Fatalf("CreateScenario: %v", err)
	}

	j := transit.Job{
		ID:         "00000000-0000-400a-8001-000000000002",
		Kind:       "compile",
		Status:     transit.JobStatusQueued,
		ScenarioID: &sc.ID,
	}
	if err := repo.CreateJob(ctx, j); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := repo.UpdateJobStatus(ctx, j.ID, transit.JobStatusRunning, ""); err != nil {
		t.Fatalf("UpdateJobStatus running: %v", err)
	}

	// Nothing has succeeded yet.
	if _, ok, err := repo.GetLatestSucceededJob(ctx, sc.Slug, "compile"); err != nil || ok {
		t.Fatalf("GetLatestSucceededJob before completion: ok=%v err=%v, want ok=false", ok, err)
	}

	graph := transit.TransitGraph{Services: []transit.ServiceGraph{
		{ServiceID: "svc-1", WaitSecs: 90, Edges: []transit.Edge{
			{FromSlug: "a", ToSlug: "b", Seconds: 120},
			{FromSlug: "b", ToSlug: "a", Seconds: 130},
		}},
	}}
	if err := repo.CompleteJob(ctx, j.ID, graph); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}

	byID, ok, err := repo.GetJobByID(ctx, j.ID)
	if err != nil || !ok {
		t.Fatalf("GetJobByID: ok=%v err=%v", ok, err)
	}
	if byID.Status != transit.JobStatusSucceeded {
		t.Errorf("GetJobByID: status = %q, want succeeded", byID.Status)
	}
	if byID.Result == nil || len(byID.Result.Services) != 1 || byID.Result.Services[0].ServiceID != "svc-1" {
		t.Fatalf("GetJobByID: result = %+v, want the compiled graph", byID.Result)
	}

	bySlug, ok, err := repo.GetLatestSucceededJob(ctx, sc.Slug, "compile")
	if err != nil || !ok {
		t.Fatalf("GetLatestSucceededJob: ok=%v err=%v", ok, err)
	}
	if bySlug.ID != j.ID || bySlug.Result == nil || len(bySlug.Result.Services[0].Edges) != 2 {
		t.Errorf("GetLatestSucceededJob = %+v, want job %s with its graph", bySlug, j.ID)
	}

	// A different kind for the same scenario must not match.
	if _, ok, err := repo.GetLatestSucceededJob(ctx, sc.Slug, "compute"); err != nil || ok {
		t.Fatalf("GetLatestSucceededJob wrong kind: ok=%v err=%v, want ok=false", ok, err)
	}
	// An unknown scenario slug must not match.
	if _, ok, err := repo.GetLatestSucceededJob(ctx, "no-such-scenario", "compile"); err != nil || ok {
		t.Fatalf("GetLatestSucceededJob unknown scenario: ok=%v err=%v, want ok=false", ok, err)
	}
}

// TestWritableDomainRoundTrip proves arbitrary domain rows (not just the seed)
// can be written through the repository and read back with their embedded
// aggregates intact, using native types (jsonb geometry, uuid FKs, boolean).
func TestWritableDomainRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	const (
		scenarioID = "00000000-0000-4001-8002-000000000001"
		routeID    = "00000000-0000-4002-8002-000000000001"
		vehicleID  = "00000000-0000-4003-8002-000000000001"
		stationA   = "00000000-0000-4005-8002-000000000001"
		stationB   = "00000000-0000-4005-8002-000000000002"
		serviceID  = "00000000-0000-4004-8002-000000000001"
	)

	if err := repo.CreateScenario(ctx, transit.Scenario{
		ID: scenarioID, Slug: "test-net", Name: "Test Net", Status: "draft",
	}); err != nil {
		t.Fatalf("CreateScenario: %v", err)
	}
	if err := repo.CreateVehicleType(ctx, transit.VehicleType{
		ID: vehicleID, Name: "Test EMU", Propulsion: "electric", MaxSpeedKMH: 200,
		AccelerationMS2: 0.5, DecelerationMS2: 0.6, FloorHeight: "high",
		DwellLevelS: 30, DwellStepS: 60,
	}); err != nil {
		t.Fatalf("CreateVehicleType: %v", err)
	}
	if err := repo.CreateRoute(ctx, transit.Route{
		ID: routeID, ScenarioID: ptr(scenarioID), Slug: "main", Name: "Main", Mode: "rail", Bidirectional: true,
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122, 37}, {-121, 37}}},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	for _, st := range []transit.Station{
		{ID: stationA, ScenarioID: scenarioID, Slug: "a", Name: "A", PlatformHeight: "high",
			Location: transit.GeoPoint{Type: "Point", Coordinates: []float64{-122, 37}}},
		{ID: stationB, ScenarioID: scenarioID, Slug: "b", Name: "B", PlatformHeight: "high",
			Location: transit.GeoPoint{Type: "Point", Coordinates: []float64{-121, 37}}},
	} {
		if err := repo.CreateStation(ctx, st); err != nil {
			t.Fatalf("CreateStation %s: %v", st.Slug, err)
		}
	}

	dwell := 45
	svc := transit.Service{
		ID: serviceID, ScenarioID: scenarioID, RouteID: routeID, VehicleTypeID: vehicleID,
		Name: "Test Service", Direction: "both", Active: true, Provenance: transit.ProvenanceCalibrated,
		Stops: []transit.ServiceStop{
			{StationID: stationA, Sequence: 1},
			{StationID: stationB, Sequence: 2, DwellS: &dwell},
		},
		FrequencyWindows: []transit.FrequencyWindow{
			{StartTime: "06:00", EndTime: "22:00", HeadwayS: 600},
		},
	}
	if err := repo.CreateService(ctx, svc); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	// Read the geometry back to confirm jsonb round-trips.
	routes, err := repo.ListRoutesByScenario(ctx, scenarioID)
	if err != nil || len(routes) != 1 {
		t.Fatalf("ListRoutesByScenario: n=%d err=%v", len(routes), err)
	}
	if routes[0].Geometry.Type != "LineString" || len(routes[0].Geometry.Coordinates) != 2 {
		t.Errorf("route geometry did not round-trip: %+v", routes[0].Geometry)
	}

	services, err := repo.ListServicesByScenario(ctx, scenarioID)
	if err != nil || len(services) != 1 {
		t.Fatalf("ListServicesByScenario: n=%d err=%v", len(services), err)
	}
	rs := services[0]
	if len(rs.Stops) != 2 {
		t.Fatalf("service stops: want 2, got %d", len(rs.Stops))
	}
	if rs.Stops[0].DwellS != nil {
		t.Error("stop 1 dwell should be nil")
	}
	if rs.Stops[1].DwellS == nil || *rs.Stops[1].DwellS != 45 {
		t.Errorf("stop 2 dwell: want 45, got %v", rs.Stops[1].DwellS)
	}
	if len(rs.FrequencyWindows) != 1 || rs.FrequencyWindows[0].HeadwayS != 600 {
		t.Errorf("frequency windows did not round-trip: %+v", rs.FrequencyWindows)
	}
}

// An admin-ingested route is standalone: no scenario, addressed by slug, with
// per-segment physics stored alongside its geometry. This is the persistence
// half of SPA-75's "geometry + per-segment physics persists and can be read
// back" — the jsonb segment array and the nullable scenario_id are both things
// the in-memory handler tests cannot prove.
func TestIngestedRouteRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	const routeID = "00000000-0000-4002-8004-000000000001"
	want := transit.Route{
		ID:            routeID,
		Slug:          "ingested-alignment",
		Name:          "Ingested Alignment",
		Mode:          "rail",
		Bidirectional: true,
		Geometry: transit.GeoLineString{
			Type:        "LineString",
			Coordinates: [][]float64{{-122.4, 37.79}, {-122.3, 37.70}, {-122.2, 37.60}},
		},
		Segments: []transit.RouteSegment{
			{CantMM: 150, CurveRadiusM: 1200, GradePct: 1.2},
			{CantMM: 0, CurveRadiusM: 0, GradePct: -0.8},
		},
	}
	if err := repo.CreateRoute(ctx, want); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}

	got, ok, err := repo.GetRouteBySlug(ctx, want.Slug)
	if err != nil || !ok {
		t.Fatalf("GetRouteBySlug: ok=%v err=%v", ok, err)
	}
	if got.ScenarioID != nil {
		t.Errorf("scenario_id = %v, want NULL for a standalone route", *got.ScenarioID)
	}
	if got.Name != want.Name || got.Mode != want.Mode || !got.Bidirectional {
		t.Errorf("route metadata did not round-trip: %+v", got)
	}
	if len(got.Geometry.Coordinates) != 3 {
		t.Fatalf("geometry did not round-trip: %+v", got.Geometry)
	}
	if len(got.Segments) != len(want.Segments) {
		t.Fatalf("segments: want %d, got %d", len(want.Segments), len(got.Segments))
	}
	for i := range want.Segments {
		if got.Segments[i] != want.Segments[i] {
			t.Errorf("segment %d = %+v, want %+v", i, got.Segments[i], want.Segments[i])
		}
	}

	// A slug is globally unique, so a second route cannot claim it.
	dup := want
	dup.ID = "00000000-0000-4002-8004-000000000002"
	if err := repo.CreateRoute(ctx, dup); err == nil {
		t.Error("CreateRoute with a duplicate slug: want error, got nil")
	}

	if _, ok, err := repo.GetRouteBySlug(ctx, "no-such-route"); ok || err != nil {
		t.Errorf("unknown slug: ok=%v err=%v, want false/nil", ok, err)
	}
}

// ListRoutes backs the route picker (SPA-104): every ingested route, in a
// stable order, reduced to the three fields a picker needs. Scenario-attached
// and standalone routes are both listed — a picker offers whatever exists.
func TestListRoutesReturnsEveryIngestedRouteInSlugOrder(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	if empty, err := repo.ListRoutes(ctx); err != nil || len(empty) != 0 {
		t.Fatalf("ListRoutes on an empty database: got %+v err=%v", empty, err)
	}

	geom := transit.GeoLineString{
		Type:        "LineString",
		Coordinates: [][]float64{{-122, 37}, {-121, 37}},
	}
	for _, rt := range []transit.Route{
		{ID: "00000000-0000-4002-8006-000000000002", Slug: "z-alignment", Name: "Z Alignment", Mode: "rail", Geometry: geom},
		{ID: "00000000-0000-4002-8006-000000000001", Slug: "a-alignment", Name: "A Alignment", Mode: "metro", Geometry: geom},
	} {
		if err := repo.CreateRoute(ctx, rt); err != nil {
			t.Fatalf("CreateRoute %s: %v", rt.Slug, err)
		}
	}

	got, err := repo.ListRoutes(ctx)
	if err != nil {
		t.Fatalf("ListRoutes: %v", err)
	}
	want := []transit.RouteSummary{
		{Slug: "a-alignment", Name: "A Alignment", Mode: "metro"},
		{Slug: "z-alignment", Name: "Z Alignment", Mode: "rail"},
	}
	if len(got) != len(want) {
		t.Fatalf("ListRoutes = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("route %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A route with no authored physics must read back as an empty segment list
// rather than a NULL that breaks decoding.
func TestRouteWithoutSegmentsRoundTrips(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	if err := repo.CreateRoute(ctx, transit.Route{
		ID:       "00000000-0000-4002-8005-000000000001",
		Slug:     "no-physics",
		Name:     "No Physics",
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122, 37}, {-121, 37}}},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}

	got, ok, err := repo.GetRouteBySlug(ctx, "no-physics")
	if err != nil || !ok {
		t.Fatalf("GetRouteBySlug: ok=%v err=%v", ok, err)
	}
	if len(got.Segments) != 0 {
		t.Errorf("segments = %+v, want empty", got.Segments)
	}
}
