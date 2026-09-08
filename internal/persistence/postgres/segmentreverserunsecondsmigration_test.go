package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/jackc/pgx/v5"
)

func rewindSegmentReverseRunSecondsMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`ALTER TABLE segments DROP COLUMN IF EXISTS reverse_run_seconds`,
		`DELETE FROM goose_db_version WHERE version_id = 18`)
	rewindPrerenderedIsochronesMigration(t, url)
}

func insertPreFixAsymmetricSegments(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`INSERT INTO scenarios (id, slug, name) VALUES ('`+phase1ScenarioID+`', 'ca-hsr', 'CA HSR')`,
		`INSERT INTO routes (id, scenario_id, slug, name, geometry)
		   VALUES ('`+phase1RouteID+`', '`+phase1ScenarioID+`', 'ca-hsr-phase-1', 'CA HSR Phase 1',
		           '{"type":"LineString","coordinates":[[-121.9,37.3],[-118.2,34.0]]}'::jsonb)`,
		`INSERT INTO segments (scenario_id, from_slug, to_slug, run_seconds, route_id)
		   VALUES
		     ('`+phase1ScenarioID+`', 'gilroy', 'merced', 3050, '`+phase1RouteID+`'),
		     ('`+phase1ScenarioID+`', 'bakersfield', 'palmdale', 1660, '`+phase1RouteID+`'),
		     ('`+phase1ScenarioID+`', 'sf', 'millbrae', 760, '`+phase1RouteID+`')`)
}

const (
	otherScenarioID = "00000000-0000-4001-8008-000000000001"
	otherRouteID    = "00000000-0000-4002-8008-000000000001"
)

func insertUnrelatedGilroyMerced(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`INSERT INTO scenarios (id, slug, name) VALUES ('`+otherScenarioID+`', 'other', 'Other')`,
		`INSERT INTO routes (id, scenario_id, slug, name, geometry)
		   VALUES ('`+otherRouteID+`', '`+otherScenarioID+`', 'other-route', 'Other',
		           '{"type":"LineString","coordinates":[[-121.9,37.3],[-118.2,34.0]]}'::jsonb)`,
		`INSERT INTO segments (scenario_id, from_slug, to_slug, run_seconds, route_id)
		   VALUES ('`+otherScenarioID+`', 'gilroy', 'merced', 9999, '`+otherRouteID+`')`)
}

func segmentReverseSecs(t *testing.T, url, from, to string) *int {
	t.Helper()
	return segmentReverseSecsInScenario(t, url, phase1ScenarioID, from, to)
}

func segmentReverseSecsInScenario(t *testing.T, url, scenarioID, from, to string) *int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var got *int
	if err := conn.QueryRow(ctx,
		`SELECT reverse_run_seconds FROM segments
		  WHERE scenario_id = $1 AND from_slug = $2 AND to_slug = $3`,
		scenarioID, from, to).Scan(&got); err != nil {
		t.Fatalf("%s %s→%s reverse_run_seconds: %v", scenarioID, from, to, err)
	}
	return got
}

func assertReverseSecs(t *testing.T, url, from, to string, want int) {
	t.Helper()
	got := segmentReverseSecs(t, url, from, to)
	if got == nil || *got != want {
		t.Errorf("%s→%s reverse_run_seconds: want %d, got %v", from, to, want, got)
	}
}

func TestSegmentReverseRunSecondsMigrationCorrectsAnAlreadyPopulatedScenario(t *testing.T) {
	_, url := freshRepo(t)
	rewindSegmentReverseRunSecondsMigration(t, url)
	insertPreFixAsymmetricSegments(t, url)
	insertUnrelatedGilroyMerced(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration failed over a seeded ca-hsr: %v", err)
	}

	assertReverseSecs(t, url, "gilroy", "merced", 2940)
	assertReverseSecs(t, url, "bakersfield", "palmdale", 1490)
	if got := segmentReverseSecs(t, url, "sf", "millbrae"); got != nil {
		t.Errorf("sf→millbrae reverse_run_seconds: want nil, got %v", *got)
	}
	if got := segmentReverseSecsInScenario(t, url, otherScenarioID, "gilroy", "merced"); got != nil {
		t.Errorf("unrelated gilroy→merced reverse_run_seconds: want nil, got %v", *got)
	}
}

func TestSegmentReverseRunSecondsMigrationIsANoOpOnAnEmptyDatabase(t *testing.T) {
	t.Run("unrelated pair is left alone", func(t *testing.T) {
		_, url := freshRepo(t)
		rewindSegmentReverseRunSecondsMigration(t, url)
		insertUnrelatedGilroyMerced(t, url)

		if err := postgres.Migrate(context.Background(), url); err != nil {
			t.Fatalf("migration over an unrelated gilroy→merced: %v", err)
		}

		got := segmentReverseSecsInScenario(t, url, otherScenarioID, "gilroy", "merced")
		if got != nil {
			t.Errorf("unrelated gilroy→merced reverse_run_seconds: want nil, got %v", *got)
		}
	})

	t.Run("fresh install compiles directed mountain hops", func(t *testing.T) {
		repo, _ := freshRepo(t)
		ctx := context.Background()
		seeded, err := transit.SeedIfEmpty(ctx, repo)
		if err != nil {
			t.Fatalf("SeedIfEmpty: %v", err)
		}
		if !seeded {
			t.Fatal("expected seed to run on empty database")
		}

		sc, ok, err := repo.GetScenarioBySlug(ctx, "ca-hsr")
		if err != nil || !ok {
			t.Fatalf("GetScenarioBySlug: ok=%v err=%v", ok, err)
		}
		graph, err := transit.CompileSeededScenario(ctx, repo, sc, transit.DefaultBoardingWaitPolicy())
		if err != nil {
			t.Fatalf("CompileSeededScenario: %v", err)
		}

		data := transit.CompiledGraphData{Graph: &graph}
		gotGilroy, _, _, ok := data.TravelTimeBetween("ca-hsr", "gilroy", "merced")
		if !ok {
			t.Fatal("gilroy→merced not found in compiled graph")
		}
		gotMerced, _, _, ok := data.TravelTimeBetween("ca-hsr", "merced", "gilroy")
		if !ok {
			t.Fatal("merced→gilroy not found in compiled graph")
		}
		if gotGilroy == gotMerced {
			t.Errorf("gilroy↔merced: want different durations, both %d", gotGilroy)
		}
		if gotGilroy != 3140 {
			t.Errorf("gilroy→merced: want 3140 (run_seconds 3050 + dwell 90), got %d", gotGilroy)
		}
		if gotMerced != 3030 {
			t.Errorf("merced→gilroy: want 3030 (run_seconds 2940 + dwell 90), got %d", gotMerced)
		}
	})
}

func TestSegmentReverseRunSecondsMigrationIsSafeToReRun(t *testing.T) {
	_, url := freshRepo(t)
	rewindSegmentReverseRunSecondsMigration(t, url)
	insertPreFixAsymmetricSegments(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("first migration: %v", err)
	}

	// Forget that it ran while keeping the data it wrote, so the second pass
	// meets exactly the state a YAML-seeded database would present. 00019 sits
	// above 18 now, so it must be unwound too — goose refuses to re-apply 18
	// while a later version is still recorded, and 00019 creates a table, so
	// leaving it behind would fail the re-migrate on CREATE TABLE.
	rewindPrerenderedIsochronesMigration(t, url)
	exec(t, url, `DELETE FROM goose_db_version WHERE version_id = 18`)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration re-run over the data it already wrote: %v", err)
	}

	assertReverseSecs(t, url, "gilroy", "merced", 2940)
	assertReverseSecs(t, url, "bakersfield", "palmdale", 1490)
	if got := segmentReverseSecs(t, url, "sf", "millbrae"); got != nil {
		t.Errorf("sf→millbrae reverse_run_seconds after re-run: want nil, got %v", *got)
	}
}
