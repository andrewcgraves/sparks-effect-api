package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/jackc/pgx/v5"
)

func rewindTravelModeTransitMigration(t *testing.T, url string) {
	t.Helper()
	rewindOwnedDomainModelsMigration(t, url)
	exec(t, url,
		`ALTER TABLE IF EXISTS routing_jobs
		   DROP CONSTRAINT IF EXISTS routing_jobs_mode_valid`,
		`ALTER TABLE IF EXISTS prerendered_isochrones
		   DROP CONSTRAINT IF EXISTS prerendered_isochrones_mode_valid`,
		`DELETE FROM goose_db_version WHERE version_id = 21`)
}

func modeJobID(i int) string {
	return fmt.Sprintf("00000000-0000-400b-8004-%012d", i)
}

func insertRoutingJobMode(t *testing.T, url, id, mode string) error {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx,
		`INSERT INTO routing_jobs (id, status, compile_job_id, lat, lng, budget_mins, mode)
		 VALUES ($1, 'queued', $2, 37.3, -121.9, 30, $3)`,
		id, routingCompileJobID, mode)
	return err
}

func countRoutingJobs(t *testing.T, url string) int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM routing_jobs`).Scan(&n); err != nil {
		t.Fatalf("counting routing jobs: %v", err)
	}
	return n
}

func TestTravelModeCheckAcceptsEveryModeTheEnumDoes(t *testing.T) {
	repo, url := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	modes := transit.TravelModes()
	if len(modes) == 0 {
		t.Fatal("TravelModes() is empty; there is no set to check the constraint against")
	}
	for i, mode := range modes {
		if !mode.Valid() {
			t.Fatalf("%q is in TravelModes() but not Valid; fix the enum, not the test", mode)
		}
		if err := insertRoutingJobMode(t, url, modeJobID(i), string(mode)); err != nil {
			t.Errorf("mode %q is accepted by TravelMode.Valid but refused by the database: %v", mode, err)
		}
	}
}

func TestTravelModeCheckRefusesAnythingElse(t *testing.T) {
	repo, url := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	for i, mode := range []string{"fly", "multimodal", "pedestrian", "Transit", "transit ", ""} {
		err := insertRoutingJobMode(t, url, modeJobID(i), mode)
		if err == nil {
			t.Errorf("the database stored mode %q; the CHECK should have refused it", mode)
			continue
		}
		if !strings.Contains(err.Error(), "routing_jobs_mode_valid") {
			t.Errorf("mode %q was refused, but not by the mode CHECK: %v", mode, err)
		}
	}
}

func TestRoutingJobTransitModeRoundTrips(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	j := transit.RoutingJob{
		ID:           routingJobID,
		Status:       transit.JobStatusQueued,
		CompileJobID: routingCompileJobID,
		Lat:          37.3382,
		Lng:          -121.8863,
		BudgetMins:   90,
		Mode:         transit.TravelModeTransit,
	}
	if err := repo.CreateRoutingJob(ctx, &j); err != nil {
		t.Fatalf("CreateRoutingJob in transit mode: %v", err)
	}

	got, ok, err := repo.GetRoutingJobByID(ctx, j.ID)
	if err != nil || !ok {
		t.Fatalf("GetRoutingJobByID: ok=%v err=%v", ok, err)
	}
	if got.Mode != transit.TravelModeTransit {
		t.Errorf("mode = %q, want %q", got.Mode, transit.TravelModeTransit)
	}
}

func TestPrerenderedIsochroneTransitMode(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)
	seedPrerenderedScenario(t, repo)

	entry := transit.PrerenderedIsochrone{
		ID: prerenderedEntryA, ScenarioSlug: prerenderedSlug,
		Label: "San Jose — 90 min by transit",
		Lat:   37.3297, Lng: -121.9020, BudgetMins: 90,
		Mode:   transit.TravelModeTransit,
		Result: json.RawMessage(`{"opaque":true}`),
	}
	if err := repo.CreatePrerenderedIsochrone(ctx, &entry); err != nil {
		t.Fatalf("CreatePrerenderedIsochrone in transit mode: %v", err)
	}
	got, ok, err := repo.GetPrerenderedIsochrone(ctx, entry.ID)
	if err != nil || !ok {
		t.Fatalf("GetPrerenderedIsochrone: ok=%v err=%v", ok, err)
	}
	if got.Mode != transit.TravelModeTransit {
		t.Errorf("mode = %q, want %q", got.Mode, transit.TravelModeTransit)
	}

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx,
		`INSERT INTO prerendered_isochrones
		   (id, scenario_slug, label, lat, lng, budget_mins, mode, result)
		 VALUES ($1, $2, 'junk', 37.3, -121.9, 30, 'multimodal', '{}'::jsonb)`,
		prerenderedEntryB, prerenderedSlug)
	if err == nil {
		t.Fatal("the database stored a prerendered isochrone in mode 'multimodal'")
	}
	if !strings.Contains(err.Error(), "prerendered_isochrones_mode_valid") {
		t.Errorf("refused, but not by the mode CHECK: %v", err)
	}
}

func TestTravelModeCheckMigrationOverADeployedDatabase(t *testing.T) {
	repo, url := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)
	seedPrerenderedScenario(t, repo)

	// Put the database back to before 00021 and write the rows as it held them
	// then: modes checked by nothing but Go.
	rewindTravelModeTransitMigration(t, url)
	for i, mode := range []string{"walk", "bike", "drive"} {
		if err := insertRoutingJobMode(t, url, modeJobID(i), mode); err != nil {
			t.Fatalf("staging a pre-00021 routing job in mode %q: %v", mode, err)
		}
	}
	execSQL(t, url,
		`INSERT INTO prerendered_isochrones
		   (id, scenario_slug, label, lat, lng, budget_mins, mode, result)
		 VALUES ($1, $2, 'legacy', 37.3, -121.9, 30, 'walk', '{}'::jsonb)`,
		prerenderedEntryA, prerenderedSlug)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("00021 over a database already holding walk/bike/drive rows: %v", err)
	}

	// The rows are still there — this migration adds a constraint and rewrites
	// nothing — and the constraint is now in force over the table they are in.
	if n := countRoutingJobs(t, url); n != 3 {
		t.Errorf("routing_jobs holds %d rows, want the 3 staged before the migration", n)
	}
	if err := insertRoutingJobMode(t, url, modeJobID(9), "fly"); err == nil {
		t.Error("mode 'fly' stored after the migration; the CHECK is not in force")
	}
}

func TestTravelModeCheckMigrationIsSafeToReRun(t *testing.T) {
	repo, url := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	// Forget that it ran while keeping the constraints it added, so the second
	// pass meets exactly the state a deployed database presents. 00022–00024
	// are unrecorded with it: goose applies only versions above the highest
	// one recorded, so leaving any of them would make this re-run skip 00021
	// and prove nothing.
	execSQL(t, url, `DELETE FROM goose_db_version WHERE version_id IN (21, 22, 23, 24)`)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("00021 re-run over the constraints it already added: %v", err)
	}
	if err := insertRoutingJobMode(t, url, modeJobID(0), "transit"); err != nil {
		t.Errorf("transit refused after the re-run: %v", err)
	}
	if err := insertRoutingJobMode(t, url, modeJobID(1), "fly"); err == nil {
		t.Error("mode 'fly' stored after the re-run; the CHECK did not survive it")
	}

	// And the rewind every other test in this package reaches through the chain
	// must leave a database Migrate can bring forward again.
	rewindTravelModeTransitMigration(t, url)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("Migrate after rewinding 00021: %v", err)
	}
	if err := insertRoutingJobMode(t, url, modeJobID(2), "fly"); err == nil {
		t.Error("mode 'fly' stored after the rewind and re-migrate; the CHECK is missing")
	}
}
