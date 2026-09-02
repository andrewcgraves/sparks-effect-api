package postgres_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

// rewindIsochroneCacheDepartsOnMigration unwinds 00024 and is the current tail
// of the rewind chain that starts in snapmigration_test.go — 00024 is the
// highest migration today, so nothing needs unwinding above it the way every
// other link in the chain unwinds the one above. Any migration added after this
// one must extend the chain here, or every rewinding test in this package
// starts failing with goose's "missing migrations before current version".
//
// 00024 replaces the four-column primary key with a five-column UNIQUE NULLS
// NOT DISTINCT constraint, so unwinding it restores the original key as well
// as dropping the column. The cache is emptied first: two transit dates that
// 00024 allowed would collide under the restored four-column key, and a cache
// is disposable. The table-exists guard is a DO block because this runs part-way
// down a chain that drops the table — 00014's rewind does — and a rewind helper
// must not depend on the order it is reached in.
func rewindIsochroneCacheDepartsOnMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url, `
		DO $rewind$
		BEGIN
			IF to_regclass('isochrone_cache') IS NOT NULL THEN
				ALTER TABLE isochrone_cache DROP CONSTRAINT IF EXISTS isochrone_cache_key;
				DELETE FROM isochrone_cache;
				ALTER TABLE isochrone_cache DROP COLUMN IF EXISTS departs_on;
				ALTER TABLE isochrone_cache DROP CONSTRAINT IF EXISTS isochrone_cache_pkey;
				ALTER TABLE isochrone_cache
					ADD PRIMARY KEY (compile_job_id, station_slug, mode, contour_mins);
			END IF;
		END
		$rewind$`,
		`DELETE FROM goose_db_version WHERE version_id = 24`)
}

func isochroneCacheHasColumn(t *testing.T, url, column string) bool {
	t.Helper()
	return scalarCount(t, url,
		`SELECT count(*) FROM information_schema.columns
		  WHERE table_name = 'isochrone_cache' AND column_name = '`+column+`'`) == 1
}

func isochroneCacheUniqueDef(t *testing.T, url string) string {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var def string
	err = conn.QueryRow(ctx, `
		SELECT pg_get_constraintdef(c.oid)
		FROM pg_constraint c
		WHERE c.conrelid = 'isochrone_cache'::regclass
		  AND c.conname = 'isochrone_cache_key'`).Scan(&def)
	if err != nil {
		t.Fatalf("isochrone_cache_key definition: %v", err)
	}
	return def
}

// The column and the uniqueness that lets two service dates coexist are the
// whole of 00024, so both must be present after migrate and both must be gone
// after a rewind.
func TestIsochroneCacheDepartsOnMigrationAddsColumnAndUniqueKey(t *testing.T) {
	_, url := freshRepo(t)

	if !isochroneCacheHasColumn(t, url, "departs_on") {
		t.Fatal("isochrone_cache.departs_on missing after migrate")
	}
	def := isochroneCacheUniqueDef(t, url)
	if got := scalarCount(t, url,
		`SELECT count(*) FROM pg_constraint
		  WHERE conrelid = 'isochrone_cache'::regclass AND contype = 'p'`); got != 0 {
		t.Errorf("isochrone_cache still has a primary key after 00024; uniqueness moved to isochrone_cache_key, got %d pkeys", got)
	}
	want := "UNIQUE NULLS NOT DISTINCT (compile_job_id, station_slug, mode, contour_mins, departs_on)"
	if def != want {
		t.Errorf("isochrone_cache_key = %q, want %q", def, want)
	}

	rewindIsochroneCacheDepartsOnMigration(t, url)
	if isochroneCacheHasColumn(t, url, "departs_on") {
		t.Error("isochrone_cache.departs_on survived the rewind")
	}
	if got := scalarCount(t, url,
		`SELECT count(*) FROM pg_constraint
		  WHERE conrelid = 'isochrone_cache'::regclass
		    AND conname = 'isochrone_cache_key'`); got != 0 {
		t.Errorf("isochrone_cache_key survived the rewind, got %d", got)
	}
	if got := scalarCount(t, url,
		`SELECT count(*) FROM pg_constraint
		  WHERE conrelid = 'isochrone_cache'::regclass AND contype = 'p'`); got != 1 {
		t.Errorf("restored primary key after rewind: want 1, got %d", got)
	}
}

// A schema change re-applied over a database that already holds it must not
// fail — the property every migration in this package is held to, and the
// reason 00024 drops the unique before adding it.
func TestIsochroneCacheDepartsOnMigrationIsSafeToReRun(t *testing.T) {
	_, url := freshRepo(t)

	exec(t, url, `DELETE FROM goose_db_version WHERE version_id = 24`)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("re-running 00024 over the schema it already created: %v", err)
	}
	if !isochroneCacheHasColumn(t, url, "departs_on") {
		t.Fatal("isochrone_cache.departs_on missing after re-run")
	}
	want := "UNIQUE NULLS NOT DISTINCT (compile_job_id, station_slug, mode, contour_mins, departs_on)"
	if got := isochroneCacheUniqueDef(t, url); got != want {
		t.Errorf("isochrone_cache_key after re-run = %q, want %q", got, want)
	}
}

// Two transit service dates on one graph and station are two rows: the Get
// predicate the worker will add is a miss on the other date, and Put has to
// have somewhere to write the new one. Walk/bike/drive stay one row, including
// when departs_on is NULL on both.
func TestIsochroneCacheAllowsTwoTransitDatesAndStillCollidesWalk(t *testing.T) {
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
		(compile_job_id, station_slug, mode, contour_mins, geometry, departs_on)
		VALUES ($1, 'sf-transbay', $2, 30, $3, $4)`

	if _, err := conn.Exec(ctx, insert, routingCompileJobID, "transit", geometry, "2026-09-01"); err != nil {
		t.Fatalf("insert transit 2026-09-01: %v", err)
	}
	if _, err := conn.Exec(ctx, insert, routingCompileJobID, "transit", geometry, "2026-09-02"); err != nil {
		t.Fatalf("insert transit 2026-09-02: %v", err)
	}
	if _, err := conn.Exec(ctx, insert, routingCompileJobID, "transit", geometry, "2026-09-02"); err == nil {
		t.Error("a second transit row for the same date stored; uniqueness must include departs_on")
	}

	if _, err := conn.Exec(ctx, insert, routingCompileJobID, "walk", geometry, nil); err != nil {
		t.Fatalf("insert walk with NULL departs_on: %v", err)
	}
	if _, err := conn.Exec(ctx, insert, routingCompileJobID, "walk", geometry, nil); err == nil {
		t.Error("a second walk row with NULL departs_on stored; NULLS NOT DISTINCT must keep those colliding")
	}

	var n int
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM isochrone_cache WHERE compile_job_id = $1`,
		routingCompileJobID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Errorf("isochrone_cache holds %d rows, want 3 (two transit dates and one walk)", n)
	}
}
