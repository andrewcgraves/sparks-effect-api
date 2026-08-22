package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

// 00017 parks HSR Express (`active = false`) on a deployed database. The seed
// cannot deliver that fix to a database that is already populated —
// SeedIfEmpty returns early there — so these tests pin both sides: the
// migration must correct a deployed database and do nothing at all to a
// fresh one, where the seed writes the parked service moments later.
const hsrExpressID = "00000000-0000-4004-8001-000000000001"

// rewindHSRExpressParkedMigration unwinds 00017. It is no longer the tail of
// the rewind chain that starts in snapmigration_test.go — 00018 sits above it
// now, so this must unwind that first, the same reason every other link in the
// chain unwinds the migration above it before its own.
//
// 00017 only UPDATEs an existing row — it creates no tables and no columns —
// so unwinding it is just unrecording the version.
func rewindHSRExpressParkedMigration(t *testing.T, url string) {
	t.Helper()
	rewindSegmentReverseRunSecondsMigration(t, url)
	exec(t, url, `DELETE FROM goose_db_version WHERE version_id = 17`)
}

// insertPreFixHSRExpress stages ca-hsr as a deployed database held it before
// SPA-223: the scenario, vehicle type, route, and the HSR Express service
// still carrying `active = true`.
func insertPreFixHSRExpress(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`INSERT INTO scenarios (id, slug, name) VALUES ('`+phase1ScenarioID+`', 'ca-hsr', 'CA HSR')`,
		`INSERT INTO vehicle_types (id, name, max_speed_kmh, acceleration_ms2, deceleration_ms2, dwell_level_s, dwell_step_s)
		   VALUES ('00000000-0000-4003-8001-000000000001', 'HSR trainset', 350, 0.6, 0.8, 30, 10)`,
		`INSERT INTO routes (id, scenario_id, slug, name, geometry)
		   VALUES ('`+phase1RouteID+`', '`+phase1ScenarioID+`', 'ca-hsr-phase-1', 'CA HSR Phase 1',
		           '{"type":"LineString","coordinates":[[-121.9,37.3],[-118.2,34.0]]}'::jsonb)`,
		`INSERT INTO services (id, scenario_id, route_id, vehicle_type_id, name, direction, active, provenance)
		   VALUES ('`+hsrExpressID+`', '`+phase1ScenarioID+`', '`+phase1RouteID+`',
		           '00000000-0000-4003-8001-000000000001', 'HSR Express', 'both', true, 'calibrated')`)
}

// The case that actually matters in production: a seeded ca-hsr the seed will
// never revisit must come out of the migration with HSR Express parked, not
// still running.
func TestHSRExpressParkedMigrationCorrectsAnAlreadyPopulatedScenario(t *testing.T) {
	_, url := freshRepo(t)
	rewindHSRExpressParkedMigration(t, url)
	insertPreFixHSRExpress(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration failed over a seeded ca-hsr: %v", err)
	}

	if got := scalarCount(t, url,
		`SELECT count(*) FROM services WHERE id = '`+hsrExpressID+`' AND active = false`); got != 1 {
		t.Error("HSR Express was not parked (active = false) by the migration")
	}
}

// On a fresh database the migration runs before the seed, so there is no HSR
// Express service to update and it must touch nothing — the seed inserts the
// already-parked service from YAML a moment later.
func TestHSRExpressParkedMigrationIsANoOpOnAnEmptyDatabase(t *testing.T) {
	_, url := freshRepo(t)

	if got := scalarCount(t, url,
		`SELECT count(*) FROM services WHERE id = '`+hsrExpressID+`'`); got != 0 {
		t.Errorf("migration wrote an HSR Express service on an empty database: got %d", got)
	}
}

// A database that already holds HSR Express parked — seeded from YAML, then
// migrated — must come out of a re-run unchanged.
func TestHSRExpressParkedMigrationIsSafeToReRun(t *testing.T) {
	_, url := freshRepo(t)
	rewindHSRExpressParkedMigration(t, url)
	insertPreFixHSRExpress(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("first migration: %v", err)
	}

	// Forget that it ran while keeping the data it wrote, so the second pass
	// meets exactly the state a YAML-seeded database would present. 00018 and
	// 00019 sit above 17 now, so they must be forgotten too — goose refuses to
	// re-apply 17 while a later version is still recorded. 00019 goes through
	// its rewind rather than a bare DELETE: it creates a table, and leaving the
	// table behind would fail the re-migrate on CREATE TABLE.
	rewindPrerenderedIsochronesMigration(t, url)
	exec(t, url,
		`DELETE FROM goose_db_version WHERE version_id = 18`,
		`DELETE FROM goose_db_version WHERE version_id = 17`)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration re-run over the data it already wrote: %v", err)
	}

	if got := scalarCount(t, url,
		`SELECT count(*) FROM services WHERE id = '`+hsrExpressID+`' AND active = false`); got != 1 {
		t.Error("HSR Express after a re-run: want parked (active = false)")
	}
}
