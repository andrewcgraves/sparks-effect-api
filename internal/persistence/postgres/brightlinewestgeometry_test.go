package postgres_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// 00012 is left exactly as originally written rather than edited in place
// (see 00015's header for why), so it no longer carries the seed's current
// Brightline West geometry or las-vegas coordinate — those were corrected by
// SPA-222 and now live in 00015 instead. The full-geometry pin that used to
// live here as TestBrightlineWestMigrationGeometryMatchesTheSeed moved to
// TestLasVegasStationCoordinateMigrationGeometryMatchesTheSeed in
// lasvegasstationcoordinate_test.go, which pins routes.yaml to 00015 rather
// than to 00012.
//
// Victor Valley's coordinate is unaffected by that fix and 00012 is still
// its source of truth for a deployed database, so the pin for it stays here.
func TestBrightlineWestMigrationStationsMatchTheSeed(t *testing.T) {
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
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
		if st.Slug != "victor-valley" {
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
