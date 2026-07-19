package postgres_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

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
	if err := repo.CreateUser(ctx, u); err != nil {
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
		windowID   = "00000000-0000-4006-8002-000000000001"
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
		ID: routeID, ScenarioID: scenarioID, Name: "Main", Mode: "rail", Bidirectional: true,
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
			{ID: windowID, ServiceID: serviceID, StartTime: "06:00", EndTime: "22:00", HeadwayS: 600},
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
