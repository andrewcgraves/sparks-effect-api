package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

// 00015 corrects the las-vegas station coordinate and the Brightline West
// spur's terminus, both originally seeded from an uncorrected city-centroid
// geocode 13.8 km from the real FRA-cleared terminus (SPA-222). The seed
// cannot deliver that fix to a database that is already populated —
// SeedIfEmpty returns early there — so these tests pin both sides: the
// migration must correct a deployed database and do nothing at all to a
// fresh one, where the seed writes the corrected station and geometry
// moments later.

// brokenLasVegasLocation and brokenSpurGeometry stand in for the pre-fix
// values: anything that is not the corrected one is enough to prove the
// UPDATEs ran, and using the actual old station coordinate — as a two-vertex
// stub for the route, Palmdale straight to the wrong Las Vegas point — keeps
// the test's intent legible rather than an obvious sentinel.
const (
	brokenLasVegasLocation = `{"type":"Point","coordinates":[-115.136,36.174]}`
	brokenSpurGeometry     = `{"type":"LineString","coordinates":` +
		`[[-118.119,34.591],[-115.136,36.174]]}`
)

// rewindLasVegasCoordinateMigration unwinds 00015. It is no longer the tail of
// the rewind chain that starts in snapmigration_test.go — 00016 sits above it
// now, so this must unwind that first, the same reason every other link in the
// chain unwinds the migration above it before its own: goose refuses to
// re-apply 00015 while a later version (16) is still recorded.
//
// 00015 only UPDATEs existing rows — it creates no tables — so unwinding it
// is just unrecording the version.
func rewindLasVegasCoordinateMigration(t *testing.T, url string) {
	t.Helper()
	rewindLasVegasRoutingLocationMigration(t, url)
	exec(t, url, `DELETE FROM goose_db_version WHERE version_id = 15`)
}

// insertPreFixLasVegasSpur stages ca-hsr as a deployed database held it
// before this fix: the scenario, the Brightline West route carrying the old
// geometry, and the las-vegas station carrying the old coordinate.
func insertPreFixLasVegasSpur(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`INSERT INTO scenarios (id, slug, name) VALUES ('`+phase1ScenarioID+`', 'ca-hsr', 'CA HSR')`,
		`INSERT INTO routes (id, scenario_id, slug, name, geometry)
		   VALUES ('`+bwRouteID+`', '`+phase1ScenarioID+`',
		           'brightline-west-palmdale-to-las-vegas', 'Brightline West — Palmdale to Las Vegas',
		           '`+brokenSpurGeometry+`'::jsonb)`,
		`INSERT INTO stations (id, scenario_id, slug, name, location)
		   VALUES ('`+bwVegasID+`', '`+phase1ScenarioID+`', 'las-vegas', 'Las Vegas',
		           '`+brokenLasVegasLocation+`'::jsonb)`)
}

// The case that actually matters in production: a seeded ca-hsr the seed will
// never revisit must come out of the migration holding the corrected station
// and the corrected terminus, not just some geometry of the right shape.
func TestLasVegasStationCoordinateMigrationCorrectsAnAlreadyPopulatedScenario(t *testing.T) {
	_, url := freshRepo(t)
	rewindLasVegasCoordinateMigration(t, url)
	insertPreFixLasVegasSpur(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration failed over a seeded ca-hsr: %v", err)
	}

	const corrected = `[-115.1778, 36.0545]`
	if got := scalarCount(t, url,
		`SELECT count(*) FROM stations WHERE id = '`+bwVegasID+`'
		   AND location->'coordinates' = '`+corrected+`'::jsonb`); got != 1 {
		t.Error("las-vegas station was not corrected to the real terminus coordinate")
	}

	want := len(seededBrightlineWestGeometry(t).Coordinates)
	if got := spurVertexCount(t, url); got != want {
		t.Fatalf("spur vertices after the migration: want %d (as seeded), got %d", want, got)
	}

	// The vertex count alone would pass on a line of the right length ending
	// somewhere else. Check the corrected terminus is actually the last vertex
	// in the column, not merely present somewhere in it.
	if got := scalarCount(t, url,
		`SELECT count(*) FROM routes WHERE id = '`+bwRouteID+`'
		   AND geometry->'coordinates'->-1 = '`+corrected+`'::jsonb`); got != 1 {
		t.Error("the corrected spur geometry does not terminate at the real terminus")
	}
}

// On a fresh database the migration runs before the seed, so there is no
// Brightline West route or las-vegas station to update and it must touch
// nothing — the seed inserts the corrected station and geometry from YAML a
// moment later.
func TestLasVegasStationCoordinateMigrationIsANoOpOnAnEmptyDatabase(t *testing.T) {
	_, url := freshRepo(t)

	if got := scalarCount(t, url,
		`SELECT count(*) FROM stations WHERE id = '`+bwVegasID+`'`); got != 0 {
		t.Errorf("migration wrote a las-vegas station on an empty database: got %d", got)
	}
	if got := scalarCount(t, url,
		`SELECT count(*) FROM routes WHERE id = '`+bwRouteID+`'`); got != 0 {
		t.Errorf("migration wrote a Brightline West route on an empty database: got %d", got)
	}
}

// A database that already holds the corrected station and geometry — seeded
// from YAML, then migrated — must come out of a re-run unchanged.
func TestLasVegasStationCoordinateMigrationIsSafeToReRun(t *testing.T) {
	_, url := freshRepo(t)
	rewindLasVegasCoordinateMigration(t, url)
	insertPreFixLasVegasSpur(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	first := spurVertexCount(t, url)

	// Forget that it ran while keeping the data it wrote, so the second pass
	// meets exactly the state a YAML-seeded database would present.
	rewindLasVegasCoordinateMigration(t, url)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration re-run over the data it already wrote: %v", err)
	}

	if got := spurVertexCount(t, url); got != first {
		t.Errorf("spur vertices after a re-run: want %d, got %d", first, got)
	}
	if got := scalarCount(t, url, `SELECT count(*) FROM stations WHERE id = '`+bwVegasID+`'`); got != 1 {
		t.Errorf("las-vegas stations after a re-run: want 1, got %d", got)
	}
}

func spurVertexCount(t *testing.T, url string) int {
	t.Helper()
	return scalarCount(t, url,
		`SELECT jsonb_array_length(geometry->'coordinates') FROM routes
		   WHERE id = '`+bwRouteID+`'`)
}
