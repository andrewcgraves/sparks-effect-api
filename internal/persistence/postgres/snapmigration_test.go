package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/jackc/pgx/v5"
)

func rewindSnapMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`ALTER TABLE user_services DROP CONSTRAINT IF EXISTS user_services_stops_have_slugs`,
		`ALTER TABLE user_services DROP CONSTRAINT IF EXISTS user_services_stops_are_snapped`)
	rewindJobTargetsMigration(t, url)
	rewindTo(t, url, 7)
}

func rewindJobTargetsMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`DROP INDEX IF EXISTS jobs_user_service_id_idx`,
		`DROP INDEX IF EXISTS jobs_user_scenario_id_idx`,
		`ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_one_target`,
		`ALTER TABLE jobs DROP COLUMN IF EXISTS compiled_service_ids`,
		`ALTER TABLE jobs DROP COLUMN IF EXISTS user_service_id`,
		`ALTER TABLE jobs DROP COLUMN IF EXISTS user_scenario_id`)
	rewindTo(t, url, 9)
	rewindInterchangePairsMigration(t, url)
}

func rewindInterchangePairsMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`ALTER TABLE user_scenarios DROP COLUMN IF EXISTS interchange_pairs`)
	rewindTo(t, url, 10)
	rewindSegmentRouteIDMigration(t, url)
}

func exec(t *testing.T, url string, statements ...string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	for _, stmt := range statements {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
}

func insertLegacyUserService(t *testing.T, url, stopsJSON string) {
	t.Helper()
	exec(t, url, `INSERT INTO user_services (id, slug, route_id, owner_id, name, vehicle, stops)
		VALUES ('`+usServiceID+`', 'legacy', '`+usRouteID+`', '`+usOwnerID+`', 'Legacy',
		        '{"max_speed_kmh":320,"acceleration_ms2":1.1,"deceleration_ms2":1.3,"dwell_s":45}',
		        '`+stopsJSON+`')`)
}

func TestSnapMigrationRefusesAPreSnapRow(t *testing.T) {
	_, _, url := userServiceFixture(t)
	rewindSnapMigration(t, url)
	insertLegacyUserService(t, url, `[
		{"name":"On route","lat":37.0,"lng":-121.8,"seq":0},
		{"name":"Gilroy","lat":37.01,"lng":-121.4,"seq":1}
	]`)

	err := postgres.Migrate(context.Background(), url)
	if err == nil {
		t.Fatal("migration accepted a pre-snap row; it must refuse and let a human snap it")
	}
	for _, want := range []string{"user_services", "1 row"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("migration error %q does not mention %q", err, want)
		}
	}
}

func TestSnapMigrationRefusesAnOnRouteRowToo(t *testing.T) {
	_, _, url := userServiceFixture(t)
	rewindSnapMigration(t, url)
	// Both stops sit exactly on route us-route-0 (the line lat 37, west to east).
	insertLegacyUserService(t, url, `[
		{"name":"A","lat":37.0,"lng":-121.8,"seq":0},
		{"name":"B","lat":37.0,"lng":-121.4,"seq":1}
	]`)

	if err := postgres.Migrate(context.Background(), url); err == nil {
		t.Fatal("migration accepted a pre-snap row because its stops were on the alignment; " +
			"the rule is that no pre-snap row passes, whatever its coordinates")
	}
}

func TestSnapMigrationRunsOnAnEmptyTable(t *testing.T) {
	_, _, url := userServiceFixture(t)
	rewindSnapMigration(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration failed on an empty user_services: %v", err)
	}
}

func TestSnappedStopsConstraintRejectsAnUnsnappedStop(t *testing.T) {
	_, _, url := userServiceFixture(t)

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	_, err = conn.Exec(ctx, `INSERT INTO user_services (id, slug, route_id, owner_id, name, vehicle, stops)
		VALUES ('`+usServiceID+`', 'unsnapped', '`+usRouteID+`', '`+usOwnerID+`', 'Unsnapped',
		        '{"max_speed_kmh":320,"acceleration_ms2":1.1,"deceleration_ms2":1.3,"dwell_s":45}',
		        '[{"name":"A","slug":"unsnapped--a","lat":37.0,"lng":-121.8,"seq":0,"chainage_m":0,"offset_m":0},
		          {"name":"B","slug":"unsnapped--b","lat":37.0,"lng":-121.4,"seq":1}]')`)
	if err == nil {
		t.Fatal("a stop with no chainage_m was accepted")
	}
	if !strings.Contains(err.Error(), "user_services_stops_are_snapped") {
		t.Fatalf("want the snapped-stops constraint to reject it, got %v", err)
	}
}

func TestSnappedStopsConstraintAcceptsWhatTheModelWrites(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)

	svc := sampleUserService()
	for i := range svc.Stops {
		svc.Stops[i].ChainageM = float64(i) * 1000
		svc.Stops[i].OffsetM = 12.5
	}
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService with snapped stops: %v", err)
	}
}
