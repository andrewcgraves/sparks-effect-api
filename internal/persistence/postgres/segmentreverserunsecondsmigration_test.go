package postgres_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

// 00018 adds reverse_run_seconds on segments and writes the two CA HSR
// mountain-hop overrides. The seed cannot deliver that fix to a database that
// is already populated — SeedIfEmpty returns early there — so these tests pin
// both sides: the migration must correct a deployed database and do nothing at
// all to a fresh one, where the seed writes the overrides from YAML moments
// later.

// rewindSegmentReverseRunSecondsMigration unwinds 00018 and is the current
// tail of the rewind chain that starts in snapmigration_test.go — 00018 is the
// highest migration today, so nothing needs unwinding above it the way every
// other link in the chain unwinds the one above.
//
// 00018 adds a column and then UPDATEs it, so unwinding it both drops the
// column (undoing the ALTER) and unrecords the version — a plain DELETE from
// goose_db_version would leave the column behind and the next Migrate call
// would see it already exists.
func rewindSegmentReverseRunSecondsMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`ALTER TABLE segments DROP COLUMN IF EXISTS reverse_run_seconds`,
		`DELETE FROM goose_db_version WHERE version_id = 18`)
}

// insertPreFixAsymmetricSegments stages ca-hsr as a deployed database held it
// before SPA-245: the scenario, its Phase 1 route, the two mountain hops
// carrying only a forward run time, and a symmetric hop that must stay NULL.
func insertPreFixAsymmetricSegments(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`INSERT INTO scenarios (id, slug, name) VALUES ('`+phase1ScenarioID+`', 'ca-hsr', 'CA HSR')`,
		`INSERT INTO routes (id, scenario_id, slug, name, geometry)
		   VALUES ('`+phase1RouteID+`', '`+phase1ScenarioID+`', 'ca-hsr-phase-1', 'CA HSR Phase 1',
		           '{"type":"LineString","coordinates":[[-121.9,37.3],[-118.2,34.0]]}'::jsonb)`,
		`INSERT INTO segments (scenario_id, from_slug, to_slug, run_seconds, route_id)
		   VALUES
		     ('`+phase1ScenarioID+`', 'gilroy', 'merced', 3050, '`+phase1RouteID+`'),
		     ('`+phase1ScenarioID+`', 'bakersfield', 'palmdale', 1660, '`+phase1RouteID+`'),
		     ('`+phase1ScenarioID+`', 'sf', 'millbrae', 760, '`+phase1RouteID+`')`)
}

func segmentReverseSecs(t *testing.T, url, from, to string) *int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var got *int
	if err := conn.QueryRow(ctx,
		`SELECT reverse_run_seconds FROM segments WHERE from_slug = $1 AND to_slug = $2`,
		from, to).Scan(&got); err != nil {
		t.Fatalf("%s→%s reverse_run_seconds: %v", from, to, err)
	}
	return got
}

func assertReverseSecs(t *testing.T, url, from, to string, want int) {
	t.Helper()
	got := segmentReverseSecs(t, url, from, to)
	if got == nil || *got != want {
		t.Errorf("%s→%s reverse_run_seconds: want %d, got %v", from, to, want, got)
	}
}

// The case that actually matters in production: a seeded ca-hsr the seed will
// never revisit must come out of the migration holding the two reverse
// overrides, not mirroring the forward times.
func TestSegmentReverseRunSecondsMigrationCorrectsAnAlreadyPopulatedScenario(t *testing.T) {
	_, url := freshRepo(t)
	rewindSegmentReverseRunSecondsMigration(t, url)
	insertPreFixAsymmetricSegments(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration failed over a seeded ca-hsr: %v", err)
	}

	assertReverseSecs(t, url, "gilroy", "merced", 2940)
	assertReverseSecs(t, url, "bakersfield", "palmdale", 1490)
	if got := segmentReverseSecs(t, url, "sf", "millbrae"); got != nil {
		t.Errorf("sf→millbrae reverse_run_seconds: want nil, got %v", *got)
	}
}

// On a fresh database the migration runs before the seed, so there are no
// segment rows to update and it must touch nothing — the seed inserts the
// overrides from YAML a moment later. The ALTER TABLE still runs (the column
// must exist for that insert), so this only asserts the UPDATE's half is a
// no-op.
func TestSegmentReverseRunSecondsMigrationIsANoOpOnAnEmptyDatabase(t *testing.T) {
	_, url := freshRepo(t)

	if got := scalarCount(t, url, `SELECT count(*) FROM segments`); got != 0 {
		t.Errorf("migration wrote segments on an empty database: got %d", got)
	}
}

// A database that already holds the overrides — seeded from YAML, then
// migrated — must come out of a re-run unchanged.
func TestSegmentReverseRunSecondsMigrationIsSafeToReRun(t *testing.T) {
	_, url := freshRepo(t)
	rewindSegmentReverseRunSecondsMigration(t, url)
	insertPreFixAsymmetricSegments(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("first migration: %v", err)
	}

	// Forget that it ran while keeping the data it wrote, so the second pass
	// meets exactly the state a YAML-seeded database would present.
	exec(t, url, `DELETE FROM goose_db_version WHERE version_id = 18`)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration re-run over the data it already wrote: %v", err)
	}

	assertReverseSecs(t, url, "gilroy", "merced", 2940)
	assertReverseSecs(t, url, "bakersfield", "palmdale", 1490)
	if got := segmentReverseSecs(t, url, "sf", "millbrae"); got != nil {
		t.Errorf("sf→millbrae reverse_run_seconds after re-run: want nil, got %v", *got)
	}
}
