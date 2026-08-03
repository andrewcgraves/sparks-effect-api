package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

// 00013 replaces the CA HSR Phase 1 alignment, which was stitched from its
// source features in dataset order and so teleported across the Central Valley
// Wye rather than running through it. The seed cannot deliver that fix to a
// database that is already populated — SeedIfEmpty returns early there — so
// these tests pin both sides: the migration must correct a deployed database
// and do nothing at all to a fresh one, where the seed writes the corrected
// geometry moments later.

const (
	phase1RouteID    = "00000000-0000-4002-8001-000000000001"
	phase1ScenarioID = "00000000-0000-4001-8001-000000000001"

	// brokenPhase1Geometry stands in for the pre-fix alignment: any geometry
	// that is not the corrected one is enough to prove the UPDATE ran, and a
	// two-vertex stub keeps the test's intent legible. It is the real line's
	// first and last vertices, so it is a plausible thing to find in the
	// column rather than an obvious sentinel.
	brokenPhase1Geometry = `{"type":"LineString","coordinates":` +
		`[[-122.394162,37.791331],[-117.877413,33.802311]]}`
)

// rewindPhase1GeometryMigration puts the database back to how it looked
// immediately before 00013 ran. As in rewindBrightlineWestMigration, the
// version rows go from 13 upwards: goose treats a gap below the highest
// recorded version as an out-of-order migration and refuses to run.
func rewindPhase1GeometryMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url, `DELETE FROM goose_db_version WHERE version_id >= 13`)
}

// insertPreFixCaHsrPhase1 stages ca-hsr as a deployed database held it before
// this fix: the scenario and its Phase 1 route, carrying the old geometry.
func insertPreFixCaHsrPhase1(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`INSERT INTO scenarios (id, slug, name) VALUES ('`+phase1ScenarioID+`', 'ca-hsr', 'CA HSR')`,
		`INSERT INTO routes (id, scenario_id, slug, name, geometry)
		   VALUES ('`+phase1RouteID+`', '`+phase1ScenarioID+`',
		           'ca-hsr-phase-1-san-francisco-to-anaheim', 'CA HSR Phase 1',
		           '`+brokenPhase1Geometry+`'::jsonb)`)
}

// The case that actually matters in production: a seeded ca-hsr the seed will
// never revisit must come out of the migration holding the corrected alignment,
// vertex for vertex.
func TestCaHsrPhase1MigrationCorrectsAnAlreadyPopulatedScenario(t *testing.T) {
	_, url := freshRepo(t)
	rewindPhase1GeometryMigration(t, url)
	insertPreFixCaHsrPhase1(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration failed over a seeded ca-hsr: %v", err)
	}

	want := len(seededPhase1Geometry(t).Coordinates)
	if got := phase1VertexCount(t, url); got != want {
		t.Fatalf("Phase 1 vertices after the migration: want %d (as seeded), got %d", want, got)
	}

	// The count alone would pass on a line of the right length and the wrong
	// shape, and the defect this fixes was exactly that — a valid LineString
	// going to the wrong places. Check the Central Valley Wye vertex the old
	// stitch skipped is actually in the column.
	const cvyWestPortal = `[-120.680226, 37.097578]`
	if got := scalarCount(t, url,
		`SELECT count(*) FROM routes WHERE id = '`+phase1RouteID+`'
		   AND geometry->'coordinates' @> '[`+cvyWestPortal+`]'::jsonb`); got != 1 {
		t.Error("the corrected geometry does not run through the Central Valley Wye west portal")
	}
}

// On a fresh database the migration runs before the seed, so there is no Phase 1
// route to update and it must touch nothing — the seed inserts the corrected
// geometry from YAML a moment later.
func TestCaHsrPhase1MigrationIsANoOpOnAnEmptyDatabase(t *testing.T) {
	_, url := freshRepo(t)

	if got := scalarCount(t, url, `SELECT count(*) FROM routes WHERE id = '`+phase1RouteID+`'`); got != 0 {
		t.Errorf("migration wrote rows on an empty database: got %d", got)
	}
}

// A database that already holds the corrected alignment — seeded from YAML,
// then migrated — must come out of a re-run unchanged.
func TestCaHsrPhase1MigrationIsSafeToReRun(t *testing.T) {
	_, url := freshRepo(t)
	rewindPhase1GeometryMigration(t, url)
	insertPreFixCaHsrPhase1(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	first := phase1VertexCount(t, url)

	// Forget that it ran while keeping the geometry it wrote, so the second
	// pass meets exactly the state a YAML-seeded database would present.
	rewindPhase1GeometryMigration(t, url)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration re-run over the geometry it already wrote: %v", err)
	}

	if got := phase1VertexCount(t, url); got != first {
		t.Errorf("Phase 1 vertices after a re-run: want %d, got %d", first, got)
	}
	if got := scalarCount(t, url, `SELECT count(*) FROM routes WHERE id = '`+phase1RouteID+`'`); got != 1 {
		t.Errorf("Phase 1 routes after a re-run: want 1, got %d", got)
	}
}

func phase1VertexCount(t *testing.T, url string) int {
	t.Helper()
	return scalarCount(t, url,
		`SELECT jsonb_array_length(geometry->'coordinates') FROM routes
		   WHERE id = '`+phase1RouteID+`'`)
}
