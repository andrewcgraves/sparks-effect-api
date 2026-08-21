package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// The two tables SPA-182 adds are co-owned with a repository this test suite
// cannot see: the API inserts and polls routing_jobs, the worker transitions
// them and fills isochrone_cache. What is testable here is this side's half —
// the writes and reads the API performs — plus the schema itself, since the
// worker's correctness depends on a shape only this repository defines.

const (
	routingCompileJobID = "00000000-0000-400a-8002-000000000001"
	routingUserID       = "00000000-0000-4009-8002-000000000001"
	routingJobID        = "00000000-0000-400b-8002-000000000001"
)

// rewindRoutingJobsMigration unwinds 00014.
//
// Every earlier rewind reaches this by way of
// rewindLasVegasCoordinateMigration, which unwinds 00015 — the current tail
// of the chain that starts in snapmigration_test.go — first. goose refuses to
// re-apply a migration older than the highest version already recorded, so a
// test that puts the database back before 00007 must also unrecord
// everything after it — and unrecording a migration without undoing what it
// did leaves the next Migrate trying to CREATE TABLE over tables that already
// exist.
func rewindRoutingJobsMigration(t *testing.T, url string) {
	t.Helper()
	rewindLasVegasCoordinateMigration(t, url)
	exec(t, url,
		`DROP TABLE IF EXISTS isochrone_cache`,
		`DROP TABLE IF EXISTS routing_jobs`,
		`DELETE FROM goose_db_version WHERE version_id = 14`)
}

// seedCompileJob creates the compile job a routing job must reference. Every
// routing job names one: a routing job with no graph is not a request anyone
// can answer, so the column is NOT NULL and there is no way to test around it.
func seedCompileJob(t *testing.T, repo interface {
	CreateJob(context.Context, transit.Job) error
}, id string) {
	t.Helper()
	if err := repo.CreateJob(context.Background(), transit.Job{
		ID: id, Kind: transit.JobKindCompileScenario, Status: transit.JobStatusSucceeded,
	}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
}

func TestRoutingJobsRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	j := transit.RoutingJob{
		ID:           routingJobID,
		Status:       transit.JobStatusQueued,
		CompileJobID: routingCompileJobID,
		Lat:          37.79,
		Lng:          -122.397,
		BudgetMins:   45,
		Mode:         transit.TravelModeBike,
	}
	if err := repo.CreateRoutingJob(ctx, &j); err != nil {
		t.Fatalf("CreateRoutingJob: %v", err)
	}
	// The insert fills in the database-assigned timestamps, because the 202
	// response carries the row straight back to the caller.
	if j.CreatedAt.IsZero() || j.UpdatedAt.IsZero() {
		t.Error("CreateRoutingJob left the timestamps unset; the 202 body would carry zero times")
	}

	got, ok, err := repo.GetRoutingJobByID(ctx, j.ID)
	if err != nil || !ok {
		t.Fatalf("GetRoutingJobByID: ok=%v err=%v", ok, err)
	}
	if got.Status != transit.JobStatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
	if got.CompileJobID != routingCompileJobID {
		t.Errorf("compile_job_id = %q, want %q", got.CompileJobID, routingCompileJobID)
	}
	if got.Lat != 37.79 || got.Lng != -122.397 || got.BudgetMins != 45 {
		t.Errorf("request parameters did not survive the round trip: %+v", got)
	}
	// The mode is stored in the domain's own vocabulary, not Valhalla's.
	if got.Mode != transit.TravelModeBike {
		t.Errorf("mode = %q, want %q", got.Mode, transit.TravelModeBike)
	}
	if got.Result != nil {
		t.Errorf("a queued routing job must carry no result, got %s", got.Result)
	}

	if _, ok, err := repo.GetRoutingJobByID(ctx, "00000000-0000-400b-8002-0000000000ff"); err != nil || ok {
		t.Errorf("GetRoutingJobByID on a missing id: ok=%v err=%v, want false/nil", ok, err)
	}
}

// An ownerless routing job is the public seeded isochrone. It must be storable:
// the column is nullable precisely so an unauthenticated request has somewhere
// to land.
func TestRoutingJobWithoutAnOwner(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	j := transit.RoutingJob{
		ID: routingJobID, Status: transit.JobStatusQueued,
		CompileJobID: routingCompileJobID, Mode: transit.TravelModeWalk, BudgetMins: 30,
	}
	if err := repo.CreateRoutingJob(ctx, &j); err != nil {
		t.Fatalf("CreateRoutingJob: %v", err)
	}

	got, ok, err := repo.GetRoutingJobByID(ctx, j.ID)
	if err != nil || !ok {
		t.Fatalf("GetRoutingJobByID: ok=%v err=%v", ok, err)
	}
	if got.OwnerID != nil {
		t.Errorf("owner_id = %v, want nil", *got.OwnerID)
	}
}

func TestRoutingJobWithAnOwner(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)
	if err := repo.CreateUser(ctx, transit.User{
		ID: routingUserID, Email: "routing@example.com", Name: "Routing",
	}, "hash-placeholder"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	j := transit.RoutingJob{
		ID: routingJobID, Status: transit.JobStatusQueued, CompileJobID: routingCompileJobID,
		OwnerID: ptr(routingUserID), Mode: transit.TravelModeDrive, BudgetMins: 15,
	}
	if err := repo.CreateRoutingJob(ctx, &j); err != nil {
		t.Fatalf("CreateRoutingJob: %v", err)
	}

	got, ok, err := repo.GetRoutingJobByID(ctx, j.ID)
	if err != nil || !ok {
		t.Fatalf("GetRoutingJobByID: ok=%v err=%v", ok, err)
	}
	if got.OwnerID == nil || *got.OwnerID != routingUserID {
		t.Errorf("owner_id = %v, want %q", got.OwnerID, routingUserID)
	}
}

// FailRoutingJob is the API's one transition: a publish the broker never
// confirmed. Leaving such a row queued would strand a client polling work no
// worker is ever going to see.
func TestFailRoutingJob(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	j := transit.RoutingJob{
		ID: routingJobID, Status: transit.JobStatusQueued,
		CompileJobID: routingCompileJobID, Mode: transit.TravelModeWalk, BudgetMins: 30,
	}
	if err := repo.CreateRoutingJob(ctx, &j); err != nil {
		t.Fatalf("CreateRoutingJob: %v", err)
	}

	if err := repo.FailRoutingJob(ctx, j.ID, "never enqueued"); err != nil {
		t.Fatalf("FailRoutingJob: %v", err)
	}

	got, ok, err := repo.GetRoutingJobByID(ctx, j.ID)
	if err != nil || !ok {
		t.Fatalf("GetRoutingJobByID: ok=%v err=%v", ok, err)
	}
	if got.Status != transit.JobStatusFailed || got.Error != "never enqueued" {
		t.Errorf("got %s/%q, want failed/'never enqueued'", got.Status, got.Error)
	}
	if got.UpdatedAt.Before(got.CreatedAt) {
		t.Error("updated_at should be >= created_at")
	}

	if err := repo.FailRoutingJob(ctx, "00000000-0000-400b-8002-0000000000ff", "x"); err == nil {
		t.Error("FailRoutingJob on a missing job: want error, got nil")
	}
}

// A routing job cannot outlive the compiled graph it names: without that graph
// the request is unanswerable, so the row is garbage rather than history.
func TestRoutingJobCascadesWithItsCompileJob(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	j := transit.RoutingJob{
		ID: routingJobID, Status: transit.JobStatusQueued,
		CompileJobID: routingCompileJobID, Mode: transit.TravelModeWalk, BudgetMins: 30,
	}
	if err := repo.CreateRoutingJob(ctx, &j); err != nil {
		t.Fatalf("CreateRoutingJob: %v", err)
	}

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `DELETE FROM jobs WHERE id = $1`, routingCompileJobID); err != nil {
		t.Fatalf("delete compile job: %v", err)
	}

	if _, ok, err := repo.GetRoutingJobByID(ctx, j.ID); err != nil || ok {
		t.Errorf("routing job survived its compile job: ok=%v err=%v", ok, err)
	}
}

// Deleting an account takes its routing jobs with it rather than nulling the
// owner. jobs.owner_id uses ON DELETE SET NULL, but here nil means "public", so
// the same rule would quietly turn a private job into one anyone holding the id
// could read.
func TestRoutingJobCascadesWithItsOwnerRatherThanBecomingPublic(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)
	if err := repo.CreateUser(ctx, transit.User{
		ID: routingUserID, Email: "routing@example.com", Name: "Routing",
	}, "hash-placeholder"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	j := transit.RoutingJob{
		ID: routingJobID, Status: transit.JobStatusQueued, CompileJobID: routingCompileJobID,
		OwnerID: ptr(routingUserID), Mode: transit.TravelModeWalk, BudgetMins: 30,
	}
	if err := repo.CreateRoutingJob(ctx, &j); err != nil {
		t.Fatalf("CreateRoutingJob: %v", err)
	}

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `DELETE FROM users WHERE id = $1`, routingUserID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	got, ok, err := repo.GetRoutingJobByID(ctx, j.ID)
	if err != nil {
		t.Fatalf("GetRoutingJobByID: %v", err)
	}
	if ok {
		t.Errorf("a deleted owner's routing job survived as %v-owned; nil owner means world-readable", got.OwnerID)
	}
}

// The cache table is written only by the worker, so nothing in this repository
// exercises it. Its schema is still defined here, and the worker's correctness
// depends on the key being exactly these four columns — with the tileset
// timestamp deliberately outside it, so a tile rebuild does not silently
// invalidate every cached polygon.
func TestIsochroneCacheSchema(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	rows, err := conn.Query(ctx, `
		SELECT a.attname
		FROM pg_index i
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)
		WHERE i.indrelid = 'isochrone_cache'::regclass AND i.indisprimary
		ORDER BY a.attname`)
	if err != nil {
		t.Fatalf("query primary key: %v", err)
	}
	defer rows.Close()
	var key []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatalf("scan: %v", err)
		}
		key = append(key, col)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	want := []string{"compile_job_id", "contour_mins", "mode", "station_slug"}
	if len(key) != len(want) {
		t.Fatalf("primary key = %v, want %v", key, want)
	}
	for i := range want {
		if key[i] != want[i] {
			t.Fatalf("primary key = %v, want %v", key, want)
		}
	}

	// A row keyed on those four goes in, tileset timestamp and all; a second row
	// differing only in tileset timestamp collides, which is what "diagnostic
	// only, not part of the key" means in practice.
	geometry := json.RawMessage(`{"type":"Polygon","coordinates":[]}`)
	insert := `INSERT INTO isochrone_cache
		(compile_job_id, station_slug, mode, contour_mins, geometry, tileset_at)
		VALUES ($1, 'sf-transbay', 'walk', 30, $2, $3)`
	if _, err := conn.Exec(ctx, insert, routingCompileJobID, geometry, "2026-08-01T00:00:00Z"); err != nil {
		t.Fatalf("insert cache row: %v", err)
	}
	if _, err := conn.Exec(ctx, insert, routingCompileJobID, geometry, "2026-08-02T00:00:00Z"); err == nil {
		t.Error("a differing tileset_at created a second row; it must be diagnostic, not part of the key")
	}

	// It is nullable, so a worker that cannot determine the tileset date still
	// caches the polygon rather than failing the write.
	if _, err := conn.Exec(ctx, `INSERT INTO isochrone_cache
		(compile_job_id, station_slug, mode, contour_mins, geometry)
		VALUES ($1, 'sf-transbay', 'bike', 30, $2)`, routingCompileJobID, geometry); err != nil {
		t.Fatalf("insert cache row without a tileset timestamp: %v", err)
	}
}

// CountInFlightRoutingJobs is the signal the enqueue cap decides on (SPA-219),
// so what it counts is the whole behaviour: only work a worker could still be
// doing — queued or running, and young enough to believe in.
func TestCountInFlightRoutingJobs(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	create := func(id, status string) {
		t.Helper()
		j := transit.RoutingJob{
			ID: id, Status: status, CompileJobID: routingCompileJobID,
			Mode: transit.TravelModeWalk, BudgetMins: 30,
		}
		if err := repo.CreateRoutingJob(ctx, &j); err != nil {
			t.Fatalf("CreateRoutingJob %s: %v", id, err)
		}
	}
	count := func(within time.Duration) int {
		t.Helper()
		n, err := repo.CountInFlightRoutingJobs(ctx, within)
		if err != nil {
			t.Fatalf("CountInFlightRoutingJobs: %v", err)
		}
		return n
	}

	if n := count(time.Hour); n != 0 {
		t.Fatalf("empty table counted %d in flight, want 0", n)
	}

	create("00000000-0000-400b-8002-00000000000a", transit.JobStatusQueued)
	create("00000000-0000-400b-8002-00000000000b", transit.JobStatusRunning)
	// A finished job is not backlog, however recently it finished — this is
	// what makes the cap recover on its own as the worker drains the queue.
	create("00000000-0000-400b-8002-00000000000c", transit.JobStatusSucceeded)
	create("00000000-0000-400b-8002-00000000000d", transit.JobStatusFailed)

	if n := count(time.Hour); n != 2 {
		t.Errorf("in flight = %d, want 2 (the queued and running jobs only)", n)
	}

	// Backdating past the window is how an abandoned job stops counting. Without
	// it a worker outage would leave rows queued forever and the cap tripped
	// forever with it, refusing work nothing is actually doing.
	execSQL(t, url, `UPDATE routing_jobs SET created_at = now() - interval '10 minutes'
		WHERE id = $1`, "00000000-0000-400b-8002-00000000000a")

	if n := count(5 * time.Minute); n != 1 {
		t.Errorf("in flight within 5m = %d, want 1: the backdated job should have aged out", n)
	}
	if n := count(time.Hour); n != 2 {
		t.Errorf("in flight within 1h = %d, want 2: the backdated job is still inside this window", n)
	}
}
