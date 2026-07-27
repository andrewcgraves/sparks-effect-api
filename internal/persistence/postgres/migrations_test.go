package postgres_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

// The deployed database already holds seeded segments, and SeedIfEmpty will not
// re-seed a populated one — so 00011's backfill, not the seed, is what gives
// existing rows their route id. These tests rewind 00011 and put pre-migration
// rows in front of it, so the backfill is exercised as it will actually run
// rather than only on the empty-database path.

func TestSegmentRouteIDBackfillsExistingRows(t *testing.T) {
	_, url := freshRepo(t)
	rewindSegmentRouteIDMigration(t, url)

	const (
		scenarioID = "00000000-0000-4001-9001-000000000001"
		routeID    = "00000000-0000-4002-9001-000000000001"
	)
	exec(t, url,
		`INSERT INTO scenarios (id, slug, name) VALUES ('`+scenarioID+`', 'legacy', 'Legacy')`,
		`INSERT INTO routes (id, scenario_id, slug, name, geometry)
		 VALUES ('`+routeID+`', '`+scenarioID+`', 'legacy-route', 'Legacy Route', '{}'::jsonb)`,
		`INSERT INTO segments (scenario_id, from_slug, to_slug, run_seconds)
		 VALUES ('`+scenarioID+`', 'a', 'b', 100), ('`+scenarioID+`', 'b', 'c', 200)`)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration failed over pre-migration segments: %v", err)
	}

	got := segmentRouteIDs(t, url, scenarioID)
	if len(got) != 2 {
		t.Fatalf("segments after backfill: want 2, got %d", len(got))
	}
	for from, route := range got {
		if route != routeID {
			t.Errorf("segment from %s: want route %s, got %s", from, routeID, route)
		}
	}
}

// A segment whose scenario has no route cannot be grouped and has no route to
// point at, so the migration drops it rather than failing the deploy.
func TestSegmentRouteIDDropsRowsWithNoRoute(t *testing.T) {
	_, url := freshRepo(t)
	rewindSegmentRouteIDMigration(t, url)

	const scenarioID = "00000000-0000-4001-9001-000000000002"
	exec(t, url,
		`INSERT INTO scenarios (id, slug, name) VALUES ('`+scenarioID+`', 'routeless', 'Routeless')`,
		`INSERT INTO segments (scenario_id, from_slug, to_slug, run_seconds)
		 VALUES ('`+scenarioID+`', 'a', 'b', 100)`)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration failed over a routeless segment: %v", err)
	}

	if got := segmentRouteIDs(t, url, scenarioID); len(got) != 0 {
		t.Errorf("orphaned segments: want 0 after migration, got %d", len(got))
	}
}

// segmentRouteIDs returns from_slug → route_id for a scenario's segments,
// read straight from the table: these fixtures have no travel_time_sets row,
// which the repository read path requires before it returns any segment.
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
