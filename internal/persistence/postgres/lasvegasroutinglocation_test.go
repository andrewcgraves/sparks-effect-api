package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

func rewindLasVegasRoutingLocationMigration(t *testing.T, url string) {
	t.Helper()
	rewindHSRExpressParkedMigration(t, url)
	exec(t, url,
		`ALTER TABLE stations DROP COLUMN IF EXISTS routing_location`)
	rewindTo(t, url, 16)
}

func insertPreFixLasVegasStation(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`INSERT INTO scenarios (id, slug, name) VALUES ('`+phase1ScenarioID+`', 'ca-hsr', 'CA HSR')`,
		`INSERT INTO stations (id, scenario_id, slug, name, location)
		   VALUES ('`+bwVegasID+`', '`+phase1ScenarioID+`', 'las-vegas', 'Las Vegas',
		           '{"type":"Point","coordinates":[-115.1778,36.0545]}'::jsonb)`)
}

func TestLasVegasRoutingLocationMigrationCorrectsAnAlreadyPopulatedScenario(t *testing.T) {
	_, url := freshRepo(t)
	rewindLasVegasRoutingLocationMigration(t, url)
	insertPreFixLasVegasStation(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration failed over a seeded ca-hsr: %v", err)
	}

	const anchor = `[-115.1706, 36.0545]`
	if got := scalarCount(t, url,
		`SELECT count(*) FROM stations WHERE id = '`+bwVegasID+`'
		   AND routing_location->'coordinates' = '`+anchor+`'::jsonb`); got != 1 {
		t.Error("las-vegas station was not given the routing anchor")
	}

	const terminus = `[-115.1778, 36.0545]`
	if got := scalarCount(t, url,
		`SELECT count(*) FROM stations WHERE id = '`+bwVegasID+`'
		   AND location->'coordinates' = '`+terminus+`'::jsonb`); got != 1 {
		t.Error("las-vegas station's location moved; 00016 must only touch routing_location")
	}
}

func TestLasVegasRoutingLocationMigrationIsANoOpOnAnEmptyDatabase(t *testing.T) {
	_, url := freshRepo(t)

	if got := scalarCount(t, url,
		`SELECT count(*) FROM stations WHERE id = '`+bwVegasID+`'`); got != 0 {
		t.Errorf("migration wrote a las-vegas station on an empty database: got %d", got)
	}
}

func TestLasVegasRoutingLocationMigrationIsSafeToReRun(t *testing.T) {
	_, url := freshRepo(t)
	rewindLasVegasRoutingLocationMigration(t, url)
	insertPreFixLasVegasStation(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("first migration: %v", err)
	}

	// Forget that it ran while keeping the data it wrote, so the second pass
	// meets exactly the state a YAML-seeded database would present. 00019
	// goes through its rewind rather than rewindTo alone: it creates a table,
	// and leaving the table behind would fail the re-migrate on CREATE TABLE.
	rewindPrerenderedIsochronesMigration(t, url)
	rewindTo(t, url, 16)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration re-run over the data it already wrote: %v", err)
	}

	const anchor = `[-115.1706, 36.0545]`
	if got := scalarCount(t, url,
		`SELECT count(*) FROM stations WHERE id = '`+bwVegasID+`'
		   AND routing_location->'coordinates' = '`+anchor+`'::jsonb`); got != 1 {
		t.Error("las-vegas routing anchor after a re-run: want present and correct")
	}
}
