package postgres_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

// The migration that adds the Brightline West spur (00012) exists because the
// seed cannot reach a database that is already populated: SeedIfEmpty returns
// early there, so YAML alone would leave every deployed environment on a
// single-corridor ca-hsr. These tests pin both sides of that — it must do the
// work on a seeded database and nothing at all on a fresh one, since on a fresh
// one the seed is what writes the spur moments later.

const (
	bwRouteID   = "00000000-0000-4002-8001-000000000002"
	bwServiceID = "00000000-0000-4004-8001-000000000003"
	bwVictorID  = "00000000-0000-4005-8001-00000000000b"
	bwVegasID   = "00000000-0000-4005-8001-00000000000f"
)

// rewindBrightlineWestMigration puts the database back to how it looked
// immediately before 00012 ran, so a test can stage pre-SPA-153 data and let
// goose re-apply the real migration file rather than a copy of its SQL.
//
// The version rows are dropped from 12 upwards rather than at 12 alone: goose
// refuses to run a migration older than the highest one already recorded unless
// WithAllowMissing is set, so leaving a later version behind would turn 12 into
// an "out-of-order" migration and fail the run outright.
func rewindBrightlineWestMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`DELETE FROM scenario_service WHERE service_id = '`+bwServiceID+`'`,
		`DELETE FROM services WHERE id = '`+bwServiceID+`'`,
		`DELETE FROM segments WHERE route_id = '`+bwRouteID+`'`,
		`DELETE FROM routes WHERE id = '`+bwRouteID+`'`,
		`DELETE FROM stations WHERE id IN ('`+bwVictorID+`', '`+bwVegasID+`')`,
		`DELETE FROM goose_db_version WHERE version_id >= 12`)
}

// insertPreSpa153CaHsr stages ca-hsr as a deployed database held it before this
// ticket: the scenario, its Phase 1 route, the Palmdale station the spur
// branches from, and the vehicle type the new service references.
func insertPreSpa153CaHsr(t *testing.T, url string) {
	t.Helper()
	const (
		scenarioID = "00000000-0000-4001-8001-000000000001"
		phase1ID   = "00000000-0000-4002-8001-000000000001"
		palmdaleID = "00000000-0000-4005-8001-00000000000a"
		vehicleID  = "00000000-0000-4003-8001-000000000001"
	)
	exec(t, url,
		`INSERT INTO scenarios (id, slug, name) VALUES ('`+scenarioID+`', 'ca-hsr', 'CA HSR')`,
		`INSERT INTO vehicle_types
		   (id, name, max_speed_kmh, acceleration_ms2, deceleration_ms2, dwell_level_s, dwell_step_s)
		   VALUES ('`+vehicleID+`', 'Siemens Venture EMU', 320, 0.5, 0.65, 90, 180)`,
		`INSERT INTO routes (id, scenario_id, slug, name, geometry)
		   VALUES ('`+phase1ID+`', '`+scenarioID+`', 'ca-hsr-phase-1', 'Phase 1', '{}'::jsonb)`,
		`INSERT INTO stations (id, scenario_id, slug, name, location)
		   VALUES ('`+palmdaleID+`', '`+scenarioID+`', 'palmdale', 'Palmdale',
		           '{"type":"Point","coordinates":[-118.119,34.591]}'::jsonb)`,
		`INSERT INTO segments (scenario_id, from_slug, to_slug, run_seconds, route_id)
		   VALUES ('`+scenarioID+`', 'bakersfield', 'palmdale', 1660, '`+phase1ID+`')`)
}

// The case that actually matters in production: a seeded ca-hsr that the seed
// will never revisit must come out of the migration holding the spur.
func TestBrightlineWestMigrationSeedsAnAlreadyPopulatedScenario(t *testing.T) {
	_, url := freshRepo(t)
	rewindBrightlineWestMigration(t, url)
	insertPreSpa153CaHsr(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration failed over a seeded ca-hsr: %v", err)
	}

	if got := scalarCount(t, url, `SELECT count(*) FROM routes WHERE id = '`+bwRouteID+`'`); got != 1 {
		t.Errorf("Brightline West route: want 1, got %d", got)
	}
	if got := scalarCount(t, url,
		`SELECT count(*) FROM stations WHERE slug IN ('victor-valley', 'las-vegas')`); got != 2 {
		t.Errorf("spur stations: want 2, got %d", got)
	}
	if got := scalarCount(t, url, `SELECT count(*) FROM services WHERE id = '`+bwServiceID+`'`); got != 1 {
		t.Errorf("Brightline West service: want 1, got %d", got)
	}
	if got := scalarCount(t, url,
		`SELECT count(*) FROM service_stops WHERE service_id = '`+bwServiceID+`'`); got != 3 {
		t.Errorf("service stops: want 3 (Palmdale, Victor Valley, Las Vegas), got %d", got)
	}
	if got := scalarCount(t, url,
		`SELECT count(*) FROM scenario_service WHERE service_id = '`+bwServiceID+`'`); got != 1 {
		t.Errorf("curated membership: want 1, got %d", got)
	}

	// The run times must land under the spur's own route, since that is what
	// keeps them a separate group from Phase 1's.
	if got := scalarCount(t, url,
		`SELECT count(*) FROM segments WHERE route_id = '`+bwRouteID+`'`); got != 2 {
		t.Errorf("spur segments keyed to the spur route: want 2, got %d", got)
	}
	if got := scalarCount(t, url,
		`SELECT count(*) FROM segments WHERE from_slug = 'victor-valley'
		   AND to_slug = 'las-vegas' AND run_seconds = 5310`); got != 1 {
		t.Errorf("victor-valley→las-vegas run time not written as authored, got %d rows", got)
	}
}

// On a fresh database the migration runs before the seed, so there is no ca-hsr
// row to attach to and it must write nothing — the seed inserts the spur from
// YAML a moment later, and a second copy here would double every row.
func TestBrightlineWestMigrationIsANoOpOnAnEmptyDatabase(t *testing.T) {
	_, url := freshRepo(t)

	for _, q := range []string{
		`SELECT count(*) FROM routes WHERE id = '` + bwRouteID + `'`,
		`SELECT count(*) FROM services WHERE id = '` + bwServiceID + `'`,
		`SELECT count(*) FROM stations WHERE id IN ('` + bwVictorID + `', '` + bwVegasID + `')`,
		`SELECT count(*) FROM segments WHERE route_id = '` + bwRouteID + `'`,
	} {
		if got := scalarCount(t, url, q); got != 0 {
			t.Errorf("migration wrote rows on an empty database (%s): got %d", q, got)
		}
	}
}

// A database that already holds the spur — seeded from YAML, then migrated —
// must not end up with a second copy of it.
func TestBrightlineWestMigrationDoesNotDuplicateAnExistingSpur(t *testing.T) {
	_, url := freshRepo(t)
	rewindBrightlineWestMigration(t, url)
	insertPreSpa153CaHsr(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("first migration: %v", err)
	}

	// Forget that it ran while keeping every row it wrote, so the second pass
	// meets exactly the state a YAML-seeded database would present.
	exec(t, url, `DELETE FROM goose_db_version WHERE version_id >= 12`)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration re-run over rows it already wrote: %v", err)
	}

	if got := scalarCount(t, url, `SELECT count(*) FROM segments WHERE route_id = '`+bwRouteID+`'`); got != 2 {
		t.Errorf("spur segments after a re-run: want 2, got %d", got)
	}
	if got := scalarCount(t, url,
		`SELECT count(*) FROM frequency_windows WHERE service_id = '`+bwServiceID+`'`); got != 1 {
		t.Errorf("frequency windows after a re-run: want 1, got %d", got)
	}
	if got := scalarCount(t, url,
		`SELECT count(*) FROM service_stops WHERE service_id = '`+bwServiceID+`'`); got != 3 {
		t.Errorf("service stops after a re-run: want 3, got %d", got)
	}
}

func scalarCount(t *testing.T, url, query string) int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var n int
	if err := conn.QueryRow(ctx, query).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}
