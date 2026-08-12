package postgres_test

import (
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const phase1MigrationPath = "migrations/00013_ca_hsr_phase1_geometry.sql"

var errNoLineString = errors.New("no LineString literal in " + phase1MigrationPath)

// The corrected CA HSR Phase 1 alignment is written down twice — once in
// internal/transit/data/scenarios/ca-hsr/routes.yaml for databases the seed
// reaches, once as a jsonb literal in 00013 for the deployed ones it does not.
// Two copies of the same geometry drift, and the drift is invisible: both
// databases still compile, they just disagree about where the trains go. This
// is the same pin TestBrightlineWestMigrationGeometryMatchesTheSeed puts on
// 00012, and it matters more here, because the whole point of 00013 is that a
// wrong alignment produces no error — only wrong chainage.
//
// This needs no database — it compares the files themselves.
func TestCaHsrPhase1MigrationGeometryMatchesTheSeed(t *testing.T) {
	seeded := seededPhase1Geometry(t)

	migrated, err := phase1MigrationGeometry()
	if err != nil {
		t.Fatalf("read 00013 geometry: %v", err)
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

// seededPhase1Geometry is the Phase 1 alignment as routes.yaml authors it.
func seededPhase1Geometry(t *testing.T) transit.GeoLineString {
	t.Helper()
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr scenario not found")
	}
	for _, rt := range store.GetRoutesByScenario(sc.ID) {
		if rt.Slug == "ca-hsr-phase-1-san-francisco-to-anaheim" {
			return rt.Geometry
		}
	}
	t.Fatal("seeded CA HSR Phase 1 route not found")
	return transit.GeoLineString{}
}

// phase1MigrationGeometry is the alignment 00013 carries. The literal is the
// only LineString in the file, wrapped over several lines for readability, so
// the SQL line breaks and indentation are stripped back out.
func phase1MigrationGeometry() (transit.GeoLineString, error) {
	var out transit.GeoLineString
	sql, err := os.ReadFile(phase1MigrationPath)
	if err != nil {
		return out, err
	}
	lit := regexp.MustCompile(`(?s)\{"type":"LineString".*?\}`).Find(sql)
	if lit == nil {
		return out, errNoLineString
	}
	compact := regexp.MustCompile(`\s+`).ReplaceAll(lit, nil)
	if err := json.Unmarshal(compact, &out); err != nil {
		return out, err
	}
	return out, nil
}
