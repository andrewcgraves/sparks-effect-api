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

// The las-vegas station's routing_location is written down twice — once in
// internal/transit/data/scenarios/ca-hsr/stations.yaml for databases the seed
// reaches, once as an UPDATE literal in 00016 for the deployed ones it does
// not — for the same reason TestLasVegasStationCoordinateMigrationStationMatchesTheSeed
// pins 00015 against the seed's `location`: two copies of the same data drift,
// and the drift is invisible, since both databases still compile.
//
// This needs no database — it compares the files themselves.
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

// rewindLasVegasRoutingLocationMigration unwinds 00016. It is no longer the
// tail of the rewind chain that starts in snapmigration_test.go — 00017 sits
// above it now, so this must unwind that first, the same reason every other
// link in the chain unwinds the migration above it before its own.
//
// 00016 adds a column and then UPDATEs it, so unwinding it both drops the
// column (undoing the ALTER) and unrecords the version — a plain DELETE from
// goose_db_version, like 00015's old rewind, would leave the column behind and
// the next Migrate call would see it already exists.
func rewindLasVegasRoutingLocationMigration(t *testing.T, url string) {
	t.Helper()
	rewindHSRExpressParkedMigration(t, url)
	exec(t, url,
		`ALTER TABLE stations DROP COLUMN IF EXISTS routing_location`,
		`DELETE FROM goose_db_version WHERE version_id = 16`)
}

// insertPreFixLasVegasStation stages ca-hsr as a deployed database held it
// before 00016 existed: the scenario and the las-vegas station, carrying its
// corrected (00015) location but no routing anchor at all — the column has
// just been dropped by the rewind above.
func insertPreFixLasVegasStation(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`INSERT INTO scenarios (id, slug, name) VALUES ('`+phase1ScenarioID+`', 'ca-hsr', 'CA HSR')`,
		`INSERT INTO stations (id, scenario_id, slug, name, location)
		   VALUES ('`+bwVegasID+`', '`+phase1ScenarioID+`', 'las-vegas', 'Las Vegas',
		           '{"type":"Point","coordinates":[-115.1778,36.0545]}'::jsonb)`)
}

// The case that actually matters in production: a seeded ca-hsr the seed will
// never revisit must come out of the migration holding the routing anchor, not
// just a routing_location column with nothing in it.
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

	// location itself must be untouched: the routing anchor stands in only for
	// the routing worker's egress isochrone, never for the station's own place.
	const terminus = `[-115.1778, 36.0545]`
	if got := scalarCount(t, url,
		`SELECT count(*) FROM stations WHERE id = '`+bwVegasID+`'
		   AND location->'coordinates' = '`+terminus+`'::jsonb`); got != 1 {
		t.Error("las-vegas station's location moved; 00016 must only touch routing_location")
	}
}

// On a fresh database the migration runs before the seed, so the las-vegas row
// does not exist yet and the UPDATE must touch nothing — the seed inserts the
// station with its routing_location from YAML a moment later. The ALTER TABLE
// still runs (the column must exist for that insert), so this only asserts the
// UPDATE's half is a no-op.
func TestLasVegasRoutingLocationMigrationIsANoOpOnAnEmptyDatabase(t *testing.T) {
	_, url := freshRepo(t)

	if got := scalarCount(t, url,
		`SELECT count(*) FROM stations WHERE id = '`+bwVegasID+`'`); got != 0 {
		t.Errorf("migration wrote a las-vegas station on an empty database: got %d", got)
	}
}

// A database that already holds the routing anchor — seeded from YAML, then
// migrated — must come out of a re-run unchanged.
func TestLasVegasRoutingLocationMigrationIsSafeToReRun(t *testing.T) {
	_, url := freshRepo(t)
	rewindLasVegasRoutingLocationMigration(t, url)
	insertPreFixLasVegasStation(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("first migration: %v", err)
	}

	// Forget that it ran while keeping the data it wrote, so the second pass
	// meets exactly the state a YAML-seeded database would present. 00017 and
	// 00018 sit above 16 now, so they must be forgotten too — goose refuses to
	// re-apply 16 while a later version is still recorded.
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
