package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const lasVegasRoutingLocationMigrationPath = "migrations/00016_las_vegas_routing_location.sql"

func TestLasVegasRoutingLocationMigrationMatchesTheSeed(t *testing.T) {
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr scenario not found")
	}

	var seeded *transit.GeoPoint
	found := false
	for _, st := range store.GetStationsByScenario(sc.ID) {
		if st.Slug == "las-vegas" {
			seeded = st.RoutingLocation
			found = true
		}
	}
	if !found {
		t.Fatal("seeded las-vegas station not found")
	}
	if seeded == nil {
		t.Fatal("seeded las-vegas station carries no routing_location")
	}

	sql, err := os.ReadFile(lasVegasRoutingLocationMigrationPath)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	want, err := json.Marshal(seeded)
	if err != nil {
		t.Fatalf("marshal las-vegas routing_location: %v", err)
	}
	if !bytes.Contains(sql, want) {
		t.Errorf("00016 does not carry las-vegas routing_location at %s", want)
	}
}

func rewindLasVegasRoutingLocationMigration(t *testing.T, url string) {
	t.Helper()
	rewindHSRExpressParkedMigration(t, url)
	exec(t, url,
		`ALTER TABLE stations DROP COLUMN IF EXISTS routing_location`,
		`DELETE FROM goose_db_version WHERE version_id = 16`)
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
	// meets exactly the state a YAML-seeded database would present. 00017,
	// 00018 and 00019 sit above 16 now, so they must be forgotten too — goose
	// refuses to re-apply 16 while a later version is still recorded. 00019
	// goes through its rewind rather than a bare DELETE: it creates a table,
	// and leaving the table behind would fail the re-migrate on CREATE TABLE.
	rewindPrerenderedIsochronesMigration(t, url)
	exec(t, url,
		`DELETE FROM goose_db_version WHERE version_id = 18`,
		`DELETE FROM goose_db_version WHERE version_id = 17`,
		`DELETE FROM goose_db_version WHERE version_id = 16`)
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
