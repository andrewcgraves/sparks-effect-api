package postgres_test

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// The Brightline West alignment is written down twice — once in
// internal/transit/data/scenarios/ca-hsr/routes.yaml for databases the seed
// reaches, once as a jsonb literal in 00012 for the deployed ones it does not
// (see brightlinewestmigration_test.go). Two copies of the same geometry drift,
// and the drift is invisible: both databases still compile, they just disagree
// about where the trains go, and only one of them is right. So the copies are
// pinned to each other here rather than by a comment asking the next editor to
// remember. This needs no database — it compares the files themselves.
func TestBrightlineWestMigrationGeometryMatchesTheSeed(t *testing.T) {
	store, err := transit.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr scenario not found")
	}
	var seeded transit.GeoLineString
	for _, rt := range store.GetRoutesByScenario(sc.ID) {
		if rt.Slug == "brightline-west-palmdale-to-las-vegas" {
			seeded = rt.Geometry
		}
	}
	if len(seeded.Coordinates) == 0 {
		t.Fatal("seeded Brightline West route not found")
	}

	sql, err := os.ReadFile("migrations/00012_brightline_west_seed.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	// The literal is the only LineString in the file, wrapped over several
	// lines for readability; strip the SQL line breaks and indentation back out.
	lit := regexp.MustCompile(`(?s)\{"type":"LineString".*?\}`).Find(sql)
	if lit == nil {
		t.Fatal("no LineString literal in 00012")
	}
	compact := regexp.MustCompile(`\s+`).ReplaceAll(lit, nil)

	var migrated transit.GeoLineString
	if err := json.Unmarshal(compact, &migrated); err != nil {
		t.Fatalf("parse migration geometry: %v", err)
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

// The same split applies to the Victor Valley station coordinate, which the
// alignment now runs through: it moved from Apple Valley town centre to the
// I-15 median at Dale Evans Parkway, and a migration that kept the old point
// would put a deployed database's stop 16 km off its own route.
func TestBrightlineWestMigrationStationsMatchTheSeed(t *testing.T) {
	store, err := transit.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr scenario not found")
	}
	sql, err := os.ReadFile("migrations/00012_brightline_west_seed.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}

	for _, st := range store.GetStationsByScenario(sc.ID) {
		if st.Slug != "victor-valley" && st.Slug != "las-vegas" {
			continue
		}
		// GeoPoint's field order is the order the migration literal is written
		// in, so the marshalled seed value is a plain substring of the SQL.
		want, err := json.Marshal(st.Location)
		if err != nil {
			t.Fatalf("marshal %s: %v", st.Slug, err)
		}
		if !bytes.Contains(sql, want) {
			t.Errorf("00012 does not carry %s at %s", st.Slug, want)
		}
	}
}
