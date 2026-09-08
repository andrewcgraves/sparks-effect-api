package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

const (
	phase1RouteID        = "00000000-0000-4002-8001-000000000001"
	phase1ScenarioID     = "00000000-0000-4001-8001-000000000001"
	brokenPhase1Geometry = `{"type":"LineString","coordinates":` +
		`[[-122.394162,37.791331],[-117.877413,33.802311]]}`
)

func rewindPhase1GeometryMigration(t *testing.T, url string) {
	t.Helper()
	rewindRoutingJobsMigration(t, url)
	exec(t, url, `DELETE FROM goose_db_version WHERE version_id = 13`)
}

func insertPreFixCaHsrPhase1(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`INSERT INTO scenarios (id, slug, name) VALUES ('`+phase1ScenarioID+`', 'ca-hsr', 'CA HSR')`,
		`INSERT INTO routes (id, scenario_id, slug, name, geometry)
		   VALUES ('`+phase1RouteID+`', '`+phase1ScenarioID+`',
		           'ca-hsr-phase-1-san-francisco-to-anaheim', 'CA HSR Phase 1',
		           '`+brokenPhase1Geometry+`'::jsonb)`)
}

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

	const cvyWestPortal = `[-120.680226, 37.097578]`
	if got := scalarCount(t, url,
		`SELECT count(*) FROM routes WHERE id = '`+phase1RouteID+`'
		   AND geometry->'coordinates' @> '[`+cvyWestPortal+`]'::jsonb`); got != 1 {
		t.Error("the corrected geometry does not run through the Central Valley Wye west portal")
	}
}

func TestCaHsrPhase1MigrationIsANoOpOnAnEmptyDatabase(t *testing.T) {
	_, url := freshRepo(t)

	if got := scalarCount(t, url, `SELECT count(*) FROM routes WHERE id = '`+phase1RouteID+`'`); got != 0 {
		t.Errorf("migration wrote rows on an empty database: got %d", got)
	}
}

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
