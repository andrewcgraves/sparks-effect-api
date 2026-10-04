package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/jackc/pgx/v5"
)

const (
	routingCompileJobID = "00000000-0000-400a-8002-000000000001"
	routingUserID       = "00000000-0000-4009-8002-000000000001"
	routingJobID        = "00000000-0000-400b-8002-000000000001"
)

func rewindRoutingJobsMigration(t *testing.T, url string) {
	t.Helper()
	rewindLasVegasCoordinateMigration(t, url)
	exec(t, url,
		`DROP TABLE IF EXISTS isochrone_cache`,
		`DROP TABLE IF EXISTS routing_jobs`)
	rewindTo(t, url, 14)
}

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
	if err := repo.CreateUser(ctx, account.User{
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

	if err := repo.FailRoutingJob(ctx, "00000000-0000-400b-8002-0000000000ff", "x"); !errors.Is(err, handler.ErrJobNotFound) {
		t.Errorf("FailRoutingJob on a missing job: %v, want ErrJobNotFound", err)
	}
}

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

func TestRoutingJobCascadesWithItsOwnerRatherThanBecomingPublic(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)
	if err := repo.CreateUser(ctx, account.User{
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
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
		WHERE c.conrelid = 'isochrone_cache'::regclass AND c.conname = 'isochrone_cache_key'
		ORDER BY a.attname`)
	if err != nil {
		t.Fatalf("query unique key: %v", err)
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

	want := []string{"budget_mins", "compile_job_id", "contour_mins", "departs_on", "mode", "station_slug"}
	if len(key) != len(want) {
		t.Fatalf("unique key = %v, want %v", key, want)
	}
	for i := range want {
		if key[i] != want[i] {
			t.Fatalf("unique key = %v, want %v", key, want)
		}
	}

	// A row keyed on those six goes in, tileset timestamp and all; a second
	// row differing only in tileset timestamp collides, which is what
	// "compared on read, not part of the key" means in practice.
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
	// caches the polygon rather than failing the write. departs_on is likewise
	// nullable so walk/bike/drive rows need not invent a service date.
	if _, err := conn.Exec(ctx, `INSERT INTO isochrone_cache
		(compile_job_id, station_slug, mode, contour_mins, geometry)
		VALUES ($1, 'sf-transbay', 'bike', 30, $2)`, routingCompileJobID, geometry); err != nil {
		t.Fatalf("insert cache row without a tileset timestamp: %v", err)
	}
}

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

func TestRoutingQueueSnapshot(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	snapshot := func() handler.RoutingQueue {
		t.Helper()
		q, err := repo.RoutingQueueSnapshot(ctx, 5*time.Minute)
		if err != nil {
			t.Fatalf("RoutingQueueSnapshot: %v", err)
		}
		return q
	}

	if q := snapshot(); q != (handler.RoutingQueue{}) {
		t.Fatalf("empty table = %+v, want nothing in flight and nothing queued", q)
	}

	const (
		older   = "00000000-0000-400b-8002-0000000000a1"
		newer   = "00000000-0000-400b-8002-0000000000a2"
		running = "00000000-0000-400b-8002-0000000000a3"
		done    = "00000000-0000-400b-8002-0000000000a4"
		ancient = "00000000-0000-400b-8002-0000000000a5"
	)
	for id, status := range map[string]string{
		older: transit.JobStatusQueued, newer: transit.JobStatusQueued,
		running: transit.JobStatusRunning, done: transit.JobStatusFailed, ancient: transit.JobStatusQueued,
	} {
		j := transit.RoutingJob{ID: id, Status: status, CompileJobID: routingCompileJobID,
			Mode: transit.TravelModeWalk, BudgetMins: 30}
		if err := repo.CreateRoutingJob(ctx, &j); err != nil {
			t.Fatalf("CreateRoutingJob %s: %v", id, err)
		}
	}
	// The running job is the oldest row in the window, so it shows the oldest
	// wait is read from queued jobs only; the ancient one is an abandoned row
	// outside it, as CountInFlightRoutingJobs ignores.
	execSQL(t, url, `UPDATE routing_jobs SET created_at = now() - interval '3 minutes' WHERE id = $1`, running)
	execSQL(t, url, `UPDATE routing_jobs SET created_at = now() - interval '2 minutes' WHERE id = $1`, older)
	execSQL(t, url, `UPDATE routing_jobs SET created_at = now() - interval '4 minutes' WHERE id = $1`, done)
	execSQL(t, url, `UPDATE routing_jobs SET created_at = now() - interval '10 minutes' WHERE id = $1`, ancient)

	q := snapshot()
	if q.InFlight != 3 {
		t.Errorf("in flight = %d, want 3: both queued jobs and the running one", q.InFlight)
	}
	if waited := time.Since(q.OldestQueuedAt); waited < 115*time.Second || waited > 125*time.Second {
		t.Errorf("oldest queued job waited %s, want about 2m", waited)
	}
}

func TestCountRoutingJobsAhead(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	const (
		running = "00000000-0000-400b-8002-0000000000b1"
		first   = "00000000-0000-400b-8002-0000000000b2"
		second  = "00000000-0000-400b-8002-0000000000b3"
		done    = "00000000-0000-400b-8002-0000000000b4"
		ancient = "00000000-0000-400b-8002-0000000000b5"
	)
	ages := map[string]struct {
		status string
		age    string
	}{
		ancient: {transit.JobStatusQueued, "10 minutes"},
		running: {transit.JobStatusRunning, "40 seconds"},
		done:    {transit.JobStatusSucceeded, "35 seconds"},
		first:   {transit.JobStatusQueued, "30 seconds"},
		second:  {transit.JobStatusQueued, "20 seconds"},
	}
	createdAt := map[string]time.Time{}
	for id, row := range ages {
		j := transit.RoutingJob{ID: id, Status: row.status, CompileJobID: routingCompileJobID,
			Mode: transit.TravelModeWalk, BudgetMins: 30}
		if err := repo.CreateRoutingJob(ctx, &j); err != nil {
			t.Fatalf("CreateRoutingJob %s: %v", id, err)
		}
		execSQL(t, url, `UPDATE routing_jobs SET created_at = now() - $2::interval WHERE id = $1`, id, row.age)
		got, ok, err := repo.GetRoutingJobByID(ctx, id)
		if err != nil || !ok {
			t.Fatalf("GetRoutingJobByID %s: found %v, %v", id, ok, err)
		}
		createdAt[id] = got.CreatedAt
	}

	ahead := func(id string) int {
		t.Helper()
		n, err := repo.CountRoutingJobsAhead(ctx, createdAt[id], 5*time.Minute)
		if err != nil {
			t.Fatalf("CountRoutingJobsAhead: %v", err)
		}
		return n
	}

	// Only in-flight jobs created earlier are ahead: not the finished one, not
	// the abandoned one outside the window, and not one created later.
	if n := ahead(second); n != 2 {
		t.Errorf("ahead of the second queued job = %d, want 2 (the running job and the first queued one)", n)
	}
	if n := ahead(first); n != 1 {
		t.Errorf("ahead of the first queued job = %d, want 1 (the running job)", n)
	}
	if n := ahead(running); n != 0 {
		t.Errorf("ahead of the running job = %d, want 0", n)
	}
}

func TestMarkRoutingJobRunning(t *testing.T) {
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

	if err := repo.MarkRoutingJobRunning(ctx, j.ID); err != nil {
		t.Fatalf("MarkRoutingJobRunning: %v", err)
	}
	got, ok, err := repo.GetRoutingJobByID(ctx, j.ID)
	if err != nil || !ok {
		t.Fatalf("GetRoutingJobByID: ok=%v err=%v", ok, err)
	}
	if got.Status != transit.JobStatusRunning {
		t.Errorf("status = %q, want running", got.Status)
	}

	// Redelivery: already running is a no-op, not an error.
	if err := repo.MarkRoutingJobRunning(ctx, j.ID); err != nil {
		t.Errorf("second MarkRoutingJobRunning: %v", err)
	}

	if err := repo.MarkRoutingJobRunning(ctx, "00000000-0000-400b-8002-0000000000ff"); !errors.Is(err, handler.ErrJobNotFound) {
		t.Errorf("missing job: %v, want ErrJobNotFound", err)
	}
}

func TestSucceedRoutingJob(t *testing.T) {
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

	want := json.RawMessage(`{"type":"FeatureCollection","features":[]}`)
	if err := repo.SucceedRoutingJob(ctx, j.ID, handler.JobSucceededBody{Result: want}); err != nil {
		t.Fatalf("SucceedRoutingJob: %v", err)
	}
	got, ok, err := repo.GetRoutingJobByID(ctx, j.ID)
	if err != nil || !ok {
		t.Fatalf("GetRoutingJobByID: ok=%v err=%v", ok, err)
	}
	if got.Status != transit.JobStatusSucceeded {
		t.Errorf("status = %q, want succeeded", got.Status)
	}
	var parsed map[string]any
	if err := json.Unmarshal(got.Result, &parsed); err != nil {
		t.Fatalf("result: %v", err)
	}
	if parsed["type"] != "FeatureCollection" {
		t.Errorf("result = %s", got.Result)
	}

	if err := repo.MarkRoutingJobRunning(ctx, j.ID); !errors.Is(err, handler.ErrJobNotFound) {
		t.Errorf("MarkRunning on succeeded job: %v, want ErrJobNotFound", err)
	}
}

func TestMarkRoutingJobRunningWillNotReopenAFailedJob(t *testing.T) {
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
	if err := repo.FailRoutingJob(ctx, j.ID, "gave up"); err != nil {
		t.Fatalf("FailRoutingJob: %v", err)
	}
	if err := repo.MarkRoutingJobRunning(ctx, j.ID); !errors.Is(err, handler.ErrJobNotFound) {
		t.Errorf("MarkRunning on failed job: %v, want ErrJobNotFound", err)
	}
	got, ok, err := repo.GetRoutingJobByID(ctx, j.ID)
	if err != nil || !ok {
		t.Fatalf("GetRoutingJobByID: ok=%v err=%v", ok, err)
	}
	if got.Status != transit.JobStatusFailed {
		t.Errorf("status = %q, want it to stay failed", got.Status)
	}
}

func TestIsochroneCacheGetPut(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	stored := handler.IsochroneKey{
		CompileJobID: routingCompileJobID, StationSlug: "station-a",
		Mode: "walk", ContourMins: 30,
	}
	missing := stored
	missing.StationSlug = "station-b"
	geom := json.RawMessage(`{"type":"FeatureCollection","features":[{"type":"Feature"}]}`)

	if err := repo.PutIsochroneCache(ctx, []handler.CachedIsochrone{
		{Key: stored, Geometry: geom, TilesetAt: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)},
	}); err != nil {
		t.Fatalf("PutIsochroneCache: %v", err)
	}

	got, err := repo.GetIsochroneCache(ctx, []handler.IsochroneKey{stored, missing})
	if err != nil {
		t.Fatalf("GetIsochroneCache: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Get returned %d entries, want 1", len(got))
	}
	if _, ok := got[stored]; !ok {
		t.Fatal("stored key missing from Get")
	}
	if _, ok := got[missing]; ok {
		t.Error("missing key was served")
	}
	// SPA-325: the worker refuses a row cut from another tileset, so the
	// stamp has to come back out exactly as it went in.
	if want := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC); !got[stored].TilesetAt.Equal(want) {
		t.Errorf("tileset_at = %v, want %v", got[stored].TilesetAt, want)
	}

	// A repeated put with no newer tileset is not an error and does not
	// overwrite a usable row.
	if err := repo.PutIsochroneCache(ctx, []handler.CachedIsochrone{
		{Key: stored, Geometry: json.RawMessage(`{"type":"Point","features":[{"type":"Feature"}]}`)},
	}); err != nil {
		t.Fatalf("second Put: %v", err)
	}
	got, err = repo.GetIsochroneCache(ctx, []handler.IsochroneKey{stored})
	if err != nil {
		t.Fatalf("Get after second Put: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(got[stored].Geometry, &parsed); err != nil {
		t.Fatalf("geometry: %v", err)
	}
	if parsed["type"] != "FeatureCollection" {
		t.Errorf("second Put overwrote the row: %s", got[stored].Geometry)
	}

	if err := repo.PutIsochroneCache(ctx, nil); err != nil {
		t.Fatalf("Put(nil): %v", err)
	}
	empty, err := repo.GetIsochroneCache(ctx, nil)
	if err != nil {
		t.Fatalf("Get(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("Get(nil) = %d, want 0", len(empty))
	}
}

func TestIsochroneCacheGetPutKeepsTransitDatesApart(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	sep := handler.IsochroneKey{
		CompileJobID: routingCompileJobID, StationSlug: "station-a",
		Mode: "transit", ContourMins: 30, DepartsOn: "2026-09-01",
	}
	oct := sep
	oct.DepartsOn = "2026-09-02"
	walk := handler.IsochroneKey{
		CompileJobID: routingCompileJobID, StationSlug: "station-a",
		Mode: "walk", ContourMins: 30,
	}
	sepGeom := json.RawMessage(`{"day":"sep"}`)
	octGeom := json.RawMessage(`{"day":"oct"}`)
	walkGeom := json.RawMessage(`{"day":"walk"}`)

	if err := repo.PutIsochroneCache(ctx, []handler.CachedIsochrone{
		{Key: sep, Geometry: sepGeom},
		{Key: oct, Geometry: octGeom},
		{Key: walk, Geometry: walkGeom},
	}); err != nil {
		t.Fatalf("PutIsochroneCache: %v", err)
	}

	got, err := repo.GetIsochroneCache(ctx, []handler.IsochroneKey{sep, oct, walk})
	if err != nil {
		t.Fatalf("GetIsochroneCache: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("Get returned %d entries, want 3", len(got))
	}
	assertDay := func(k handler.IsochroneKey, want string) {
		t.Helper()
		var parsed map[string]any
		if err := json.Unmarshal(got[k].Geometry, &parsed); err != nil {
			t.Fatalf("%s: %v", want, err)
		}
		if parsed["day"] != want {
			t.Errorf("%+v geometry day = %v, want %s", k, parsed["day"], want)
		}
	}
	assertDay(sep, "sep")
	assertDay(oct, "oct")
	assertDay(walk, "walk")

	// A walk lookup must not pick up a transit row just because they share
	// the other four key parts; NULL departs_on is not "any date".
	onlyWalk, err := repo.GetIsochroneCache(ctx, []handler.IsochroneKey{walk})
	if err != nil {
		t.Fatalf("Get walk: %v", err)
	}
	if len(onlyWalk) != 1 {
		t.Fatalf("walk lookup returned %d entries, want 1", len(onlyWalk))
	}
	if _, ok := onlyWalk[walk]; !ok {
		t.Error("walk lookup missed the NULL-date row")
	}
	// Those rows went in without a stamp; NULL comes back as the zero time,
	// which the lookup endpoint omits and the worker reads as a miss.
	if at := onlyWalk[walk].TilesetAt; !at.IsZero() {
		t.Errorf("unstamped row read back with tileset_at %v, want zero", at)
	}
}

func TestIsochroneCacheGetPutKeepsTransitBudgetsApart(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	// SPA-326: one contour, station and date, two budgets — the two chains
	// were on the platform 30 minutes apart.
	ninety := handler.IsochroneKey{
		CompileJobID: routingCompileJobID, StationSlug: "station-a",
		Mode: "transit", ContourMins: 79, DepartsOn: "2026-09-02", BudgetMins: 90,
	}
	oneTwenty := ninety
	oneTwenty.BudgetMins = 120
	walk := handler.IsochroneKey{
		CompileJobID: routingCompileJobID, StationSlug: "station-a",
		Mode: "walk", ContourMins: 79,
	}

	if err := repo.PutIsochroneCache(ctx, []handler.CachedIsochrone{
		{Key: ninety, Geometry: json.RawMessage(`{"budget":90}`)},
		{Key: walk, Geometry: json.RawMessage(`{"budget":"none"}`)},
	}); err != nil {
		t.Fatalf("PutIsochroneCache: %v", err)
	}

	got, err := repo.GetIsochroneCache(ctx, []handler.IsochroneKey{ninety, oneTwenty, walk})
	if err != nil {
		t.Fatalf("GetIsochroneCache: %v", err)
	}
	if _, ok := got[ninety]; !ok {
		t.Error("the budget-90 row was not served back for budget 90")
	}
	if _, ok := got[oneTwenty]; ok {
		t.Error("budget 90's polygon was served for budget 120's question")
	}
	if _, ok := got[walk]; !ok {
		t.Error("walk lookup missed the NULL-budget row")
	}

	if err := repo.PutIsochroneCache(ctx, []handler.CachedIsochrone{
		{Key: oneTwenty, Geometry: json.RawMessage(`{"budget":120}`)},
	}); err != nil {
		t.Fatalf("PutIsochroneCache(120): %v", err)
	}
	got, err = repo.GetIsochroneCache(ctx, []handler.IsochroneKey{ninety, oneTwenty})
	if err != nil {
		t.Fatalf("GetIsochroneCache: %v", err)
	}
	for k, want := range map[handler.IsochroneKey]float64{ninety: 90, oneTwenty: 120} {
		var parsed map[string]any
		if err := json.Unmarshal(got[k].Geometry, &parsed); err != nil {
			t.Fatalf("budget %d: %v", k.BudgetMins, err)
		}
		if parsed["budget"] != want {
			t.Errorf("budget %d served geometry for budget %v", k.BudgetMins, parsed["budget"])
		}
	}
}
