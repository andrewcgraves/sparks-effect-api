package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

// The migration that makes stops snapped (00007) chose to assert rather than
// backfill: it refuses to run if any pre-snap row exists, because snapping in
// SQL would mean a second implementation of the projection in internal/physics.
// These tests pin that choice, so a later reader finds out what the migration
// does with legacy data from a test rather than from a production incident.

// rewindSnapMigration puts the database back to how it looked immediately
// before 00007 ran: constraint gone, version row removed. goose then re-applies
// 00007 on the next Migrate, which is what lets a test put a pre-snap row in
// front of it. Doing it this way rather than migrating partially keeps the test
// honest — it runs the real migration file, not a copy of its SQL.
//
// 00008 is rewound with it, because a state before 00007 is necessarily also a
// state before 00008: leaving 00008's constraint in place would refuse the
// pre-snap rows these tests insert, and they would fail on the fixture rather
// than on the migration under test.
func rewindSnapMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`ALTER TABLE user_services DROP CONSTRAINT IF EXISTS user_services_stops_have_slugs`,
		`ALTER TABLE user_services DROP CONSTRAINT IF EXISTS user_services_stops_are_snapped`,
		`DELETE FROM goose_db_version WHERE version_id IN (7, 8)`)
	rewindJobTargetsMigration(t, url)
}

// rewindJobTargetsMigration unwinds 00009 (the job-targets columns) alongside
// the migration under test, for the same reason 00008 is unwound with 00007: a
// state before 00007/00008 is necessarily before 00009 too, and goose refuses
// to re-apply an earlier migration while a later one (version 9) is still
// recorded as applied. 00009 touches only the jobs table — empty in these
// user_services tests — so dropping and re-applying it changes nothing under
// test; it just keeps the migration order goose insists on intact.
func rewindJobTargetsMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`DROP INDEX IF EXISTS jobs_user_service_id_idx`,
		`DROP INDEX IF EXISTS jobs_user_scenario_id_idx`,
		`ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_one_target`,
		`ALTER TABLE jobs DROP COLUMN IF EXISTS compiled_service_ids`,
		`ALTER TABLE jobs DROP COLUMN IF EXISTS user_service_id`,
		`ALTER TABLE jobs DROP COLUMN IF EXISTS user_scenario_id`,
		`DELETE FROM goose_db_version WHERE version_id = 9`)
	rewindInterchangePairsMigration(t, url)
}

// rewindInterchangePairsMigration unwinds 00010 (user_scenarios'
// interchange_pairs column), for the same reason 00009 is unwound alongside
// 00007/00008: a state before any earlier migration is necessarily before
// 00010 too, and goose refuses to re-apply an earlier migration while a later
// one (version 10) is still recorded as applied. 00010 touches only
// user_scenarios — untouched by these user_services tests — so dropping and
// re-applying it changes nothing under test.
func rewindInterchangePairsMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`ALTER TABLE user_scenarios DROP COLUMN IF EXISTS interchange_pairs`,
		`DELETE FROM goose_db_version WHERE version_id = 10`)
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

// insertLegacyUserService writes a row in the pre-SPA-108 shape: raw
// coordinates, no chainage_m, no offset_m. It goes in through raw SQL because
// the Go model can no longer express a stop without them.
func insertLegacyUserService(t *testing.T, url, stopsJSON string) {
	t.Helper()
	exec(t, url, `INSERT INTO user_services (id, slug, route_id, owner_id, name, vehicle, stops)
		VALUES ('`+usServiceID+`', 'legacy', '`+usRouteID+`', '`+usOwnerID+`', 'Legacy',
		        '{"max_speed_kmh":320,"acceleration_ms2":1.1,"deceleration_ms2":1.3,"dwell_s":45}',
		        '`+stopsJSON+`')`)
}

// TestSnapMigrationRefusesAPreSnapRow is the backfill test SPA-108 asks for.
// The legacy row's second stop sits ~1.1 km north of route us-route-0, well
// past the 500 m off-route threshold — the case a backfill would have had to
// make a judgement call about with no user to ask. The chosen behaviour is that
// it does not make one: the deploy stops and a human decides.
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

// TestSnapMigrationRefusesAnOnRouteRowToo pins the other half of the choice:
// the migration stops for *any* pre-snap row, not only for one the 500 m rule
// would reject. Distance does not enter into it, because the coordinates it
// would have to measure are the ones it cannot compute without the projection
// in internal/physics. Without this test the pairing with the over-threshold
// case above would read as if the threshold were what triggered the refusal.
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

// TestSnapMigrationRunsOnAnEmptyTable is the case actually expected in every
// environment: nothing to backfill, so the migration is just the invariant.
func TestSnapMigrationRunsOnAnEmptyTable(t *testing.T) {
	_, _, url := userServiceFixture(t)
	rewindSnapMigration(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration failed on an empty user_services: %v", err)
	}
}

// TestSnappedStopsConstraintRejectsAnUnsnappedStop covers the invariant the
// migration leaves behind. Without it a stop missing chainage_m would decode to
// 0 — a real position at the start of every route — so missing data would read
// as a stop at the line's origin rather than as an error.
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

// TestSnappedStopsConstraintAcceptsWhatTheModelWrites guards against the
// constraint and the Go struct's json tags drifting apart: every stop the write
// path produces must satisfy it.
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
