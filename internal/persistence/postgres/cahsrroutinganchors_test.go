package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

const (
	maderaID      = "00000000-0000-4005-8001-000000000006"
	kingsTulareID = "00000000-0000-4005-8001-000000000008"
	bakersfieldID = "00000000-0000-4005-8001-000000000009"
)

var caHSRAnchoredStations = []struct {
	id, slug, name string
	location       string
	anchor         string
}{
	{maderaID, "madera", "Madera",
		`{"type":"Point","coordinates":[-119.986,36.936]}`,
		`[-119.99, 36.936]`},
	{kingsTulareID, "kings-tulare", "Kings/Tulare (Hanford)",
		`{"type":"Point","coordinates":[-119.592,36.335]}`,
		`[-119.584, 36.335]`},
	{bakersfieldID, "bakersfield", "Bakersfield",
		`{"type":"Point","coordinates":[-119.022,35.391]}`,
		`[-119.022, 35.389]`},
}

func rewindCAHSRRoutingAnchorsMigration(t *testing.T, url string) {
	t.Helper()
	rewindIsochroneCacheDepartsOnMigration(t, url)
	exec(t, url,
		`UPDATE stations SET routing_location = NULL
		   WHERE id IN ('`+maderaID+`', '`+kingsTulareID+`', '`+bakersfieldID+`')`)
	rewindTo(t, url, 23)
}

func insertPreFixCAHSRAnchoredStations(t *testing.T, url string) {
	t.Helper()
	stmts := []string{
		`INSERT INTO scenarios (id, slug, name) VALUES ('` + phase1ScenarioID + `', 'ca-hsr', 'CA HSR')`,
	}
	for _, st := range caHSRAnchoredStations {
		stmts = append(stmts,
			`INSERT INTO stations (id, scenario_id, slug, name, location)
			   VALUES ('`+st.id+`', '`+phase1ScenarioID+`', '`+st.slug+`', '`+st.name+`',
			           '`+st.location+`'::jsonb)`)
	}
	exec(t, url, stmts...)
}

func assertAnchored(t *testing.T, url string) {
	t.Helper()
	for _, st := range caHSRAnchoredStations {
		if got := scalarCount(t, url,
			`SELECT count(*) FROM stations WHERE id = '`+st.id+`'
			   AND routing_location->'coordinates' = '`+st.anchor+`'::jsonb`); got != 1 {
			t.Errorf("%s station was not given the routing anchor %s", st.slug, st.anchor)
		}

		// location itself must be untouched: the anchor stands in only for the
		// routing worker's Valhalla calls, never for the station's own place.
		if got := scalarCount(t, url,
			`SELECT count(*) FROM stations WHERE id = '`+st.id+`'
			   AND location = '`+st.location+`'::jsonb`); got != 1 {
			t.Errorf("%s station's location moved; 00023 must only touch routing_location", st.slug)
		}
	}
}

func TestCAHSRRoutingAnchorsMigrationCorrectsAnAlreadyPopulatedScenario(t *testing.T) {
	_, url := freshRepo(t)
	rewindCAHSRRoutingAnchorsMigration(t, url)
	insertPreFixCAHSRAnchoredStations(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration failed over a seeded ca-hsr: %v", err)
	}
	assertAnchored(t, url)
}

func TestCAHSRRoutingAnchorsMigrationIsANoOpOnAnEmptyDatabase(t *testing.T) {
	_, url := freshRepo(t)

	for _, st := range caHSRAnchoredStations {
		if got := scalarCount(t, url,
			`SELECT count(*) FROM stations WHERE id = '`+st.id+`'`); got != 0 {
			t.Errorf("migration wrote a %s station on an empty database: got %d", st.slug, got)
		}
	}
}

func TestCAHSRRoutingAnchorsMigrationIsSafeToReRun(t *testing.T) {
	_, url := freshRepo(t)
	rewindCAHSRRoutingAnchorsMigration(t, url)
	insertPreFixCAHSRAnchoredStations(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("first migration: %v", err)
	}

	// Forget that it ran while keeping the data it wrote, so the second pass
	// meets exactly the state a YAML-seeded database would present. Later
	// versions are unrecorded too: goose applies only versions above the
	// highest one recorded, so leaving any would make this re-run skip 00023.
	rewindTo(t, url, 23)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration re-run over the data it already wrote: %v", err)
	}
	assertAnchored(t, url)
}
