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

const caHSRRoutingAnchorsMigrationPath = "migrations/00020_ca_hsr_routing_locations.sql"

const (
	maderaID      = "00000000-0000-4005-8001-000000000006"
	kingsTulareID = "00000000-0000-4005-8001-000000000008"
	bakersfieldID = "00000000-0000-4005-8001-000000000009"
)

// The three stations 00020 anchors, with the `location` each must keep. The
// anchors themselves are deliberately absent: the seed is the single source
// for those, and TestCAHSRRoutingAnchorsMigrationMatchesTheSeed reads them from
// it rather than restating them here, so a third copy cannot drift from the
// other two.
var caHSRAnchoredStations = []struct {
	id, slug, name string
	location       string
}{
	{maderaID, "madera", "Madera", `{"type":"Point","coordinates":[-119.986,36.936]}`},
	{kingsTulareID, "kings-tulare", "Kings/Tulare (Hanford)", `{"type":"Point","coordinates":[-119.592,36.335]}`},
	{bakersfieldID, "bakersfield", "Bakersfield", `{"type":"Point","coordinates":[-119.022,35.391]}`},
}

// seededRoutingLocation reads one station's routing anchor out of the embedded
// YAML seed.
func seededRoutingLocation(t *testing.T, slug string) *transit.GeoPoint {
	t.Helper()
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr scenario not found")
	}
	for _, st := range store.GetStationsByScenario(sc.ID) {
		if st.Slug == slug {
			if st.RoutingLocation == nil {
				t.Fatalf("seeded %s station carries no routing_location", slug)
			}
			return st.RoutingLocation
		}
	}
	t.Fatalf("seeded %s station not found", slug)
	return nil
}

// Each anchor is written down twice — once in the YAML seed for databases the
// seed reaches, once as an UPDATE literal in 00020 for the deployed ones it
// does not — for the same reason TestLasVegasRoutingLocationMigrationMatchesTheSeed
// pins 00016: two copies of the same data drift, and the drift is invisible,
// since both databases still compile and both still answer.
//
// This needs no database — it compares the files themselves.
func TestCAHSRRoutingAnchorsMigrationMatchesTheSeed(t *testing.T) {
	sql, err := os.ReadFile(caHSRRoutingAnchorsMigrationPath)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}

	for _, st := range caHSRAnchoredStations {
		want, err := json.Marshal(seededRoutingLocation(t, st.slug))
		if err != nil {
			t.Fatalf("marshal %s routing_location: %v", st.slug, err)
		}
		if !bytes.Contains(sql, want) {
			t.Errorf("00020 does not carry %s routing_location at %s", st.slug, want)
		}
	}
}

// rewindCAHSRRoutingAnchorsMigration unwinds 00020 and is the current tail of
// the rewind chain that starts in snapmigration_test.go — 00020 is the highest
// migration today, so nothing needs unwinding above it the way every other link
// in the chain unwinds the one above. Any migration added after this one must
// extend the chain here, or every rewinding test in this package starts failing
// with goose's "missing migrations before current version".
//
// 00020 changes no schema, so unwinding it is its own Down: clear the three
// anchors it set, then unrecord the version. Clearing matters even though a
// re-run of the UPDATEs would be harmless — a test that stages a pre-00020
// database wants rows that genuinely have no anchor, not rows that already
// hold the value the migration is supposed to write.
func rewindCAHSRRoutingAnchorsMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`UPDATE stations SET routing_location = NULL
		   WHERE id IN ('`+maderaID+`', '`+kingsTulareID+`', '`+bakersfieldID+`')`,
		`DELETE FROM goose_db_version WHERE version_id = 20`)
}

// insertPreFixCAHSRAnchoredStations stages ca-hsr as a deployed database held
// it before 00020 existed: the scenario and the three stations, each carrying
// its surveyed location and no routing anchor at all.
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

// assertAnchored is the shared assertion: every station holds the anchor the
// seed says it should, and none of them has had its `location` moved.
func assertAnchored(t *testing.T, url string) {
	t.Helper()
	for _, st := range caHSRAnchoredStations {
		anchor, err := json.Marshal(seededRoutingLocation(t, st.slug).Coordinates)
		if err != nil {
			t.Fatalf("marshal %s coordinates: %v", st.slug, err)
		}
		if got := scalarCount(t, url,
			`SELECT count(*) FROM stations WHERE id = '`+st.id+`'
			   AND routing_location->'coordinates' = '`+string(anchor)+`'::jsonb`); got != 1 {
			t.Errorf("%s station was not given the routing anchor %s", st.slug, anchor)
		}

		// location itself must be untouched: the anchor stands in only for the
		// routing worker's Valhalla calls, never for the station's own place.
		if got := scalarCount(t, url,
			`SELECT count(*) FROM stations WHERE id = '`+st.id+`'
			   AND location = '`+st.location+`'::jsonb`); got != 1 {
			t.Errorf("%s station's location moved; 00020 must only touch routing_location", st.slug)
		}
	}
}

// The case that actually matters in production: a seeded ca-hsr the seed will
// never revisit must come out of the migration holding all three anchors.
func TestCAHSRRoutingAnchorsMigrationCorrectsAnAlreadyPopulatedScenario(t *testing.T) {
	_, url := freshRepo(t)
	rewindCAHSRRoutingAnchorsMigration(t, url)
	insertPreFixCAHSRAnchoredStations(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration failed over a seeded ca-hsr: %v", err)
	}
	assertAnchored(t, url)
}

// On a fresh database the migration runs before the seed, so none of the three
// rows exists yet and the UPDATEs must touch nothing — the seed inserts the
// stations with their routing_location from YAML a moment later.
func TestCAHSRRoutingAnchorsMigrationIsANoOpOnAnEmptyDatabase(t *testing.T) {
	_, url := freshRepo(t)

	for _, st := range caHSRAnchoredStations {
		if got := scalarCount(t, url,
			`SELECT count(*) FROM stations WHERE id = '`+st.id+`'`); got != 0 {
			t.Errorf("migration wrote a %s station on an empty database: got %d", st.slug, got)
		}
	}
}

// A database that already holds the anchors — seeded from YAML, then migrated —
// must come out of a re-run unchanged.
func TestCAHSRRoutingAnchorsMigrationIsSafeToReRun(t *testing.T) {
	_, url := freshRepo(t)
	rewindCAHSRRoutingAnchorsMigration(t, url)
	insertPreFixCAHSRAnchoredStations(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("first migration: %v", err)
	}

	// Forget that it ran while keeping the data it wrote, so the second pass
	// meets exactly the state a YAML-seeded database would present. Nothing
	// sits above 00020 yet, so unrecording it is enough on its own.
	exec(t, url, `DELETE FROM goose_db_version WHERE version_id = 20`)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration re-run over the data it already wrote: %v", err)
	}
	assertAnchored(t, url)
}
