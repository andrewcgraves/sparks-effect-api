package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/jackc/pgx/v5"
)

func rewindSegmentRouteIDMigration(t *testing.T, url string) {
	t.Helper()
	rewindBrightlineWestMigration(t, url)
	exec(t, url,
		`DROP INDEX IF EXISTS segments_route_id_idx`,
		`ALTER TABLE segments DROP COLUMN IF EXISTS route_id`,
		`DELETE FROM goose_db_version WHERE version_id = 11`)
}

const (
	segScenarioID = "00000000-0000-4001-9001-000000000001"
	segRouteID    = "00000000-0000-4002-9001-000000000001"
)

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
