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
