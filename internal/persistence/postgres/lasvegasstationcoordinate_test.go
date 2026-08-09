package postgres_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const lasVegasCoordinateMigrationPath = "migrations/00015_las_vegas_station_coordinate.sql"

var errNoLineStringIn00015 = errors.New("no LineString literal in " + lasVegasCoordinateMigrationPath)

// The corrected Las Vegas station coordinate and Brightline West spur
// geometry are written down twice — once in
// internal/transit/data/scenarios/ca-hsr/{stations,routes}.yaml for databases
// the seed reaches, once as UPDATE literals in 00015 for the deployed ones it
// does not (see lasvegasstationcoordinatemigration_test.go). Two copies of
// the same data drift, and the drift is invisible: both databases still
// compile, they just disagree about where the station and the trains are.
// This is the same pin TestBrightlineWestMigrationGeometryMatchesTheSeed puts
// on 00012 and TestCaHsrPhase1MigrationGeometryMatchesTheSeed puts on 00013 —
// 00012 itself is deliberately left alone (see 00015's header), so the pin
// for the spur's geometry and the las-vegas station now lives here instead.
//
// This needs no database — it compares the files themselves.
func TestLasVegasStationCoordinateMigrationGeometryMatchesTheSeed(t *testing.T) {
	seeded := seededBrightlineWestGeometry(t)

	migrated, err := lasVegasCoordinateMigrationGeometry()
	if err != nil {
		t.Fatalf("read 00015 geometry: %v", err)
	}

	if len(migrated.Coordinates) != len(seeded.Coordinates) {
		t.Fatalf("vertex count: migration has %d, seed has %d",
			len(migrated.Coordinates), len(seeded.Coordinates))
	}
	for i := range seeded.Coordinates {
		if !slices.Equal(seeded.Coordinates[i], migrated.Coordinates[i]) {
			t.Fatalf("vertex %d differs: seed %v, migration %v",
				i, seeded.Coordinates[i], migrated.Coordinates[i])
		}
	}
}

func TestLasVegasStationCoordinateMigrationStationMatchesTheSeed(t *testing.T) {
	store, err := transit.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr scenario not found")
	}

	var seeded transit.GeoPoint
	found := false
	for _, st := range store.GetStationsByScenario(sc.ID) {
		if st.Slug == "las-vegas" {
			seeded = st.Location
			found = true
		}
	}
	if !found {
		t.Fatal("seeded las-vegas station not found")
	}

	sql, err := os.ReadFile(lasVegasCoordinateMigrationPath)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	// GeoPoint's field order is the order the migration literal is written in,
	// so the marshalled seed value is a plain substring of the SQL.
	want, err := json.Marshal(seeded)
	if err != nil {
		t.Fatalf("marshal las-vegas location: %v", err)
	}
	if !bytes.Contains(sql, want) {
		t.Errorf("00015 does not carry las-vegas at %s", want)
	}
}

// seededBrightlineWestGeometry is the Brightline West spur alignment as
// routes.yaml authors it.
func seededBrightlineWestGeometry(t *testing.T) transit.GeoLineString {
	t.Helper()
	store, err := transit.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr scenario not found")
	}
	for _, rt := range store.GetRoutesByScenario(sc.ID) {
		if rt.Slug == "brightline-west-palmdale-to-las-vegas" {
			return rt.Geometry
		}
	}
	t.Fatal("seeded Brightline West route not found")
	return transit.GeoLineString{}
}

// lasVegasCoordinateMigrationGeometry is the alignment 00015 carries. The
// literal is the only LineString in the file, wrapped over several lines for
// readability, so the SQL line breaks and indentation are stripped back out.
func lasVegasCoordinateMigrationGeometry() (transit.GeoLineString, error) {
	var out transit.GeoLineString
	sql, err := os.ReadFile(lasVegasCoordinateMigrationPath)
	if err != nil {
		return out, err
	}
	lit := regexp.MustCompile(`(?s)\{"type":"LineString".*?\}`).Find(sql)
	if lit == nil {
		return out, errNoLineStringIn00015
	}
	compact := regexp.MustCompile(`\s+`).ReplaceAll(lit, nil)
	if err := json.Unmarshal(compact, &out); err != nil {
		return out, err
	}
	return out, nil
}
