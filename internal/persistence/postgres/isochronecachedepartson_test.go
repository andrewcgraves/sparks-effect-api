package postgres_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/jackc/pgx/v5"
)

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
