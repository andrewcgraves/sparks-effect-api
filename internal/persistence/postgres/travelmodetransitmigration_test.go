package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// 00021 adds 'transit' to the travel modes and, for the first time, makes the
// database hold the set at all (SPA-248). Both halves need a real Postgres:
// that the new mode stores, and that a mode outside the set no longer can.
//
// The second half is the one no in-memory fake could show, and the one worth
// having. transit.TravelMode calls itself the single definition of the set "so
// the request validator, the queue message, and the database cannot drift
// apart"; until this migration the database enforced nothing, so that was a
// statement about Go code only.

// rewindTravelModeTransitMigration unwinds 00021, unwinding the migration above
// it first the way every link in this chain does. 00022–00024 sit above it, so
// the tail of the rewind chain that starts in snapmigration_test.go is now
// rewindIsochroneCacheDepartsOnMigration. Goose refuses to re-apply a migration
// older than the highest version recorded, so anything rewinding a migration
// below this one must unrecord those — rewindBoardingWaitOverrideMigration does
// that by calling this.
//
// 00021 adds constraints, so unwinding it drops them as well as unrecording the
// version. Leaving them behind would let the next Migrate's DROP-then-ADD
// succeed as a no-op, which hides a rewind that did not actually rewind.
//
// ALTER TABLE IF EXISTS because this runs part-way down a chain that drops
// tables — prerendered_isochrones is gone once 00019's rewind has finished — and
// a rewind helper must not depend on the order it is reached in.
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

// modeJobID is a distinct routing job id per case, so a table-driven test can
// insert many rows without the primary key deciding the outcome.
func modeJobID(i int) string {
	return fmt.Sprintf("00000000-0000-400b-8004-%012d", i)
}

// insertRoutingJobMode inserts a routing job with mode set to whatever raw
// string is asked for, bypassing the Go enum the way a second writer or a hand
// at a psql prompt would. Asking what the database itself permits is the whole
// point, so this deliberately does not go through the repository.
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

// Every mode the Go enum accepts must be storable. This is the pairing that
// keeps the constraint honest: the enum and the CHECK are one set written twice
// in two languages, and only a test can compare them.
//
// It walks transit.TravelModes() rather than the four modes written out here,
// so a mode added to the enum without being added to 00021 fails here. Written
// out, this test would be a third copy of the set and would simply not cover
// the new mode — which is the whole failure it exists to catch.
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

// What the constraint is for. Each of these is a plausible way a bad mode
// arrives: a mode nobody has, Valhalla's spelling of this very mode leaking
// back across the worker boundary, another of its costing names, the wrong
// case, a stray space, and the empty string NOT NULL alone would have allowed.
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

// The mode this ticket exists for, through the path the API actually uses
// rather than raw SQL: it must survive the round trip in the domain's own
// vocabulary, never translated to Valhalla's "multimodal" on this side.
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

// prerendered_isochrones.mode is the other column 00021 constrains, and it is
// written by a different path than routing_jobs (the admin POST and the
// seeder), so it gets its own pair of assertions rather than being assumed.
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

// The case that matters in production: adding a CHECK validates every existing
// row, and migrations run on boot, so a deployed database full of rows in the
// three old modes must come through this one rather than failing the deploy.
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

// A schema change re-applied over what it already wrote must not fail — the
// property every migration in this package is held to, and the reason 00021
// drops each constraint before adding it.
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
