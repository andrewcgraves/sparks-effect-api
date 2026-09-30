package postgres_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/jackc/pgx/v5"
)

const currentIsochroneCacheKey = "UNIQUE NULLS NOT DISTINCT (compile_job_id, station_slug, mode, contour_mins, departs_on, budget_mins)"

// rewindIsochroneCacheBudgetMinsMigration is 00029's Down, and unrecords it.
func rewindIsochroneCacheBudgetMinsMigration(t *testing.T, url string) {
	t.Helper()
	// Guarded on the column rather than the table: an earlier rewind in the
	// same chain may already have undone this one, and 00024's key cannot be
	// restored once 00024's own rewind has dropped departs_on.
	exec(t, url, `
		DO $rewind$
		BEGIN
			IF EXISTS (SELECT 1 FROM information_schema.columns
			            WHERE table_name = 'isochrone_cache' AND column_name = 'budget_mins') THEN
				ALTER TABLE isochrone_cache DROP CONSTRAINT IF EXISTS isochrone_cache_key;
				DELETE FROM isochrone_cache WHERE budget_mins IS NOT NULL;
				ALTER TABLE isochrone_cache DROP COLUMN budget_mins;
				ALTER TABLE isochrone_cache
					ADD CONSTRAINT isochrone_cache_key
					UNIQUE NULLS NOT DISTINCT (compile_job_id, station_slug, mode, contour_mins, departs_on);
			END IF;
		END
		$rewind$`)
	rewindTo(t, url, 29)
}

func TestIsochroneCacheBudgetMinsMigrationAddsColumnAndUniqueKey(t *testing.T) {
	_, url := freshRepo(t)

	if !isochroneCacheHasColumn(t, url, "budget_mins") {
		t.Fatal("isochrone_cache.budget_mins missing after migrate")
	}
	if got := isochroneCacheUniqueDef(t, url); got != currentIsochroneCacheKey {
		t.Errorf("isochrone_cache_key = %q, want %q", got, currentIsochroneCacheKey)
	}

	rewindIsochroneCacheBudgetMinsMigration(t, url)
	if isochroneCacheHasColumn(t, url, "budget_mins") {
		t.Error("isochrone_cache.budget_mins survived the rewind")
	}
	want := "UNIQUE NULLS NOT DISTINCT (compile_job_id, station_slug, mode, contour_mins, departs_on)"
	if got := isochroneCacheUniqueDef(t, url); got != want {
		t.Errorf("isochrone_cache_key after rewind = %q, want 00024's %q", got, want)
	}
}

func TestIsochroneCacheBudgetMinsMigrationIsSafeToReRun(t *testing.T) {
	_, url := freshRepo(t)

	rewindTo(t, url, 29)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("re-running 00029 over the schema it already created: %v", err)
	}
	if !isochroneCacheHasColumn(t, url, "budget_mins") {
		t.Fatal("isochrone_cache.budget_mins missing after re-run")
	}
	if got := isochroneCacheUniqueDef(t, url); got != currentIsochroneCacheKey {
		t.Errorf("isochrone_cache_key after re-run = %q, want %q", got, currentIsochroneCacheKey)
	}
}

func TestIsochroneCacheBudgetMinsMigrationClearsTransitRowsOnly(t *testing.T) {
	repo, url := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)
	rewindIsochroneCacheBudgetMinsMigration(t, url)

	// Rows as a deployed database holds them before 00029: a transit row
	// keyed on a date alone, and a walk row with neither.
	geometry := `{"type":"Polygon","coordinates":[]}`
	exec(t, url,
		`INSERT INTO isochrone_cache
			(compile_job_id, station_slug, mode, contour_mins, geometry, departs_on)
		 VALUES ('`+routingCompileJobID+`', 'sf-transbay', 'transit', 79, '`+geometry+`', '2026-09-02')`,
		`INSERT INTO isochrone_cache
			(compile_job_id, station_slug, mode, contour_mins, geometry)
		 VALUES ('`+routingCompileJobID+`', 'sf-transbay', 'walk', 79, '`+geometry+`')`)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if got := scalarCount(t, url,
		`SELECT count(*) FROM isochrone_cache WHERE mode = 'transit'`); got != 0 {
		t.Errorf("%d budget-less transit rows survived 00029; no transit lookup can ever match them", got)
	}
	if got := scalarCount(t, url,
		`SELECT count(*) FROM isochrone_cache WHERE mode = 'walk'`); got != 1 {
		t.Errorf("walk rows after 00029 = %d, want 1 — the budget does not touch them", got)
	}
}

func TestIsochroneCacheAllowsTwoTransitBudgetsAndStillCollidesWalk(t *testing.T) {
	repo, url := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	geometry := json.RawMessage(`{"type":"Polygon","coordinates":[]}`)
	insert := `INSERT INTO isochrone_cache
		(compile_job_id, station_slug, mode, contour_mins, geometry, departs_on, budget_mins)
		VALUES ($1, 'sf-transbay', $2, 79, $3, $4, $5)`

	if _, err := conn.Exec(ctx, insert, routingCompileJobID, "transit", geometry, "2026-09-02", 90); err != nil {
		t.Fatalf("insert transit budget 90: %v", err)
	}
	if _, err := conn.Exec(ctx, insert, routingCompileJobID, "transit", geometry, "2026-09-02", 120); err != nil {
		t.Fatalf("insert transit budget 120 at the same contour: %v", err)
	}
	if _, err := conn.Exec(ctx, insert, routingCompileJobID, "transit", geometry, "2026-09-02", 120); err == nil {
		t.Error("a second transit row for the same budget stored; uniqueness must include budget_mins")
	}

	if _, err := conn.Exec(ctx, insert, routingCompileJobID, "walk", geometry, nil, nil); err != nil {
		t.Fatalf("insert walk with NULL budget_mins: %v", err)
	}
	if _, err := conn.Exec(ctx, insert, routingCompileJobID, "walk", geometry, nil, nil); err == nil {
		t.Error("a second walk row with NULL budget_mins stored; NULLS NOT DISTINCT must keep those colliding")
	}

	if got := scalarCount(t, url,
		`SELECT count(*) FROM isochrone_cache WHERE compile_job_id = '`+routingCompileJobID+`'`); got != 3 {
		t.Errorf("isochrone_cache holds %d rows, want 3 (two transit budgets and one walk)", got)
	}
}
