package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

const (
	brokenLasVegasLocation = `{"type":"Point","coordinates":[-115.136,36.174]}`
	brokenSpurGeometry     = `{"type":"LineString","coordinates":` +
		`[[-118.119,34.591],[-115.136,36.174]]}`
)

func rewindLasVegasCoordinateMigration(t *testing.T, url string) {
	t.Helper()
	rewindLasVegasRoutingLocationMigration(t, url)
	rewindTo(t, url, 15)
}

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
	if got := scalarCount(t, url,
		`SELECT count(*) FROM routes WHERE id = '`+bwRouteID+`'
		   AND geometry->'coordinates'->-1 = '`+corrected+`'::jsonb`); got != 1 {
		t.Error("the corrected spur geometry does not terminate at the real terminus")
	}
}

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
