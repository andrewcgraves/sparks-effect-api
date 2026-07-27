package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

// The migration that keys segments by route (00011) backfills from the
// scenario's single route, and asserts that premise rather than assuming it —
// the deployed database is already seeded, and SeedIfEmpty will not re-seed a
// populated one, so this migration is the only thing that gives existing rows
// their route. These tests pin both halves of that choice, so a later reader
// finds out what it does with legacy data from a test rather than from a
// production incident.

// rewindSegmentRouteIDMigration puts the database back to how it looked
// immediately before 00011 ran: column and index gone, version row removed.
// goose then re-applies 00011 on the next Migrate, which is what lets a test
// put pre-migration segments in front of it. Doing it this way rather than
// migrating partially keeps the test honest — it runs the real migration file,
// not a copy of its SQL.
//
// It is the tail of the rewind chain in snapmigration_test.go: every earlier
// rewind ends here, because goose refuses to re-apply an earlier migration
// while a later one is still recorded as applied.
func rewindSegmentRouteIDMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`DROP INDEX IF EXISTS segments_route_id_idx`,
		`ALTER TABLE segments DROP COLUMN IF EXISTS route_id`,
		`DELETE FROM goose_db_version WHERE version_id = 11`)
}

const (
	segScenarioID = "00000000-0000-4001-9001-000000000001"
	segRouteID    = "00000000-0000-4002-9001-000000000001"
)

// insertLegacySegments writes segments in the pre-SPA-152 shape — no route_id.
// It goes in through raw SQL because the column is NOT NULL afterwards, so the
// Go model can no longer express a segment without one.
func insertLegacySegments(t *testing.T, url string) {
	t.Helper()
	exec(t, url, `INSERT INTO segments (scenario_id, from_slug, to_slug, run_seconds)
		VALUES ('`+segScenarioID+`', 'a', 'b', 100), ('`+segScenarioID+`', 'b', 'c', 200)`)
}

func insertSegmentScenario(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`INSERT INTO scenarios (id, slug, name) VALUES ('`+segScenarioID+`', 'legacy', 'Legacy')`)
}

func insertSegmentRoute(t *testing.T, url, id, slug string) {
	t.Helper()
	exec(t, url, `INSERT INTO routes (id, scenario_id, slug, name, geometry)
		VALUES ('`+id+`', '`+segScenarioID+`', '`+slug+`', 'Route '||'`+slug+`', '{}'::jsonb)`)
}

// TestSegmentRouteIDBackfillsExistingRows is the case actually expected in
// every environment: one seeded scenario, one route, segments that predate the
// column and must come out the other side pointing at it.
func TestSegmentRouteIDBackfillsExistingRows(t *testing.T) {
	_, url := freshRepo(t)
	rewindSegmentRouteIDMigration(t, url)
	insertSegmentScenario(t, url)
	insertSegmentRoute(t, url, segRouteID, "legacy-route")
	insertLegacySegments(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration failed over pre-migration segments: %v", err)
	}

	got := segmentRouteIDs(t, url, segScenarioID)
	if len(got) != 2 {
		t.Fatalf("segments after backfill: want 2, got %d", len(got))
	}
	for from, route := range got {
		if route != segRouteID {
			t.Errorf("segment from %s: want route %s, got %s", from, segRouteID, route)
		}
	}
}

// A scenario with several routes is the case this ticket exists to unlock, and
// the one where a plain UPDATE would mis-key every segment of the other
// corridors without saying so. Which alignment a span belongs to lives in the
// authored YAML, out of SQL's reach — so the deploy stops and a human decides.
func TestSegmentRouteIDRefusesAMultiRouteScenario(t *testing.T) {
	_, url := freshRepo(t)
	rewindSegmentRouteIDMigration(t, url)
	insertSegmentScenario(t, url)
	insertSegmentRoute(t, url, segRouteID, "legacy-route")
	insertSegmentRoute(t, url, "00000000-0000-4002-9001-000000000002", "second-route")
	insertLegacySegments(t, url)

	err := postgres.Migrate(context.Background(), url)
	if err == nil {
		t.Fatal("migration guessed a route for a multi-route scenario; " +
			"it must refuse and let a human key the segments from the authored YAML")
	}
	for _, want := range []string{segScenarioID, "2 route"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("migration error %q does not mention %q", err, want)
		}
	}
}

// A scenario with no route leaves rows that cannot satisfy NOT NULL. Deleting
// them would silently destroy seeded run times SeedIfEmpty will never restore,
// so this refuses too — the segments survive for a human to key.
func TestSegmentRouteIDRefusesARoutelessScenario(t *testing.T) {
	_, url := freshRepo(t)
	rewindSegmentRouteIDMigration(t, url)
	insertSegmentScenario(t, url)
	insertLegacySegments(t, url)

	err := postgres.Migrate(context.Background(), url)
	if err == nil {
		t.Fatal("migration accepted a scenario with no route; it must refuse rather than " +
			"drop segments that cannot be re-seeded")
	}
	if !strings.Contains(err.Error(), segScenarioID) {
		t.Errorf("migration error %q does not name the offending scenario", err)
	}

	// The rows the migration refused are still there to be keyed by hand.
	var count int
	conn, connErr := pgx.Connect(context.Background(), url)
	if connErr != nil {
		t.Fatalf("connect: %v", connErr)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if scanErr := conn.QueryRow(context.Background(),
		`SELECT count(*) FROM segments WHERE scenario_id = $1`, segScenarioID).Scan(&count); scanErr != nil {
		t.Fatalf("count segments: %v", scanErr)
	}
	if count != 2 {
		t.Errorf("segments after refused migration: want 2 preserved, got %d", count)
	}
}

// segmentRouteIDs returns from_slug → route_id for a scenario's segments, read
// straight from the table: these fixtures have no travel_time_sets row, which
// the repository read path requires before it returns any segment.
func segmentRouteIDs(t *testing.T, url, scenarioID string) map[string]string {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	rows, err := conn.Query(ctx,
		`SELECT from_slug, route_id FROM segments WHERE scenario_id = $1`, scenarioID)
	if err != nil {
		t.Fatalf("query segments: %v", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var from, route string
		if err := rows.Scan(&from, &route); err != nil {
			t.Fatalf("scan segment: %v", err)
		}
		out[from] = route
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("segment rows: %v", err)
	}
	return out
}
