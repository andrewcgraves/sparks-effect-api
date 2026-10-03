package postgres_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/jackc/pgx/v5"
)

const (
	reuseOtherCompileJobID = "00000000-0000-400a-8002-000000000002"
	reuseOtherUserID       = "00000000-0000-4009-8002-000000000002"
)

var (
	reuseTileset      = time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC)
	reuseNewerTileset = time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC)
	reuseResult       = json.RawMessage(`{"type":"FeatureCollection","features":[]}`)
)

// reuseRequest is the request every case below repeats, varying one thing.
func reuseRequest() transit.RoutingJob {
	return transit.RoutingJob{
		CompileJobID: routingCompileJobID,
		Lat:          37.791234,
		Lng:          -122.397654,
		BudgetMins:   60,
		Mode:         transit.TravelModeTransit,
	}
}

func reuseRepo(t *testing.T) *postgres.Repo {
	t.Helper()
	repo, _ := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)
	seedCompileJob(t, repo, reuseOtherCompileJobID)
	for _, u := range []account.User{
		{ID: routingUserID, Email: "reuse-a@example.com", Name: "A"},
		{ID: reuseOtherUserID, Email: "reuse-b@example.com", Name: "B"},
	} {
		if err := repo.CreateUser(context.Background(), u, "hash-placeholder"); err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
	}
	return repo
}

// succeededJob inserts j and completes it with done, the way the worker would.
func succeededJob(t *testing.T, repo *postgres.Repo, id string, j transit.RoutingJob, done handler.JobSucceeded) {
	t.Helper()
	ctx := context.Background()
	j.ID = id
	j.Status = transit.JobStatusQueued
	if err := repo.CreateRoutingJob(ctx, &j); err != nil {
		t.Fatalf("CreateRoutingJob: %v", err)
	}
	if done.Result == nil {
		done.Result = reuseResult
	}
	if err := repo.SucceedRoutingJob(ctx, id, done); err != nil {
		t.Fatalf("SucceedRoutingJob: %v", err)
	}
}

func stamped() handler.JobSucceeded {
	return handler.JobSucceeded{TilesetAt: reuseTileset}
}

func findReusable(t *testing.T, repo *postgres.Repo, want transit.RoutingJob) (transit.RoutingJob, bool) {
	t.Helper()
	got, ok, err := repo.FindReusableRoutingJob(context.Background(), want)
	if err != nil {
		t.Fatalf("FindReusableRoutingJob: %v", err)
	}
	return got, ok
}

func TestFindReusableRoutingJob_servesAnIdenticalRepeat(t *testing.T) {
	repo := reuseRepo(t)
	succeededJob(t, repo, routingJobID, reuseRequest(), stamped())

	got, ok := findReusable(t, repo, reuseRequest())
	if !ok {
		t.Fatal("an identical repeat of a succeeded job was not reused")
	}
	if got.ID != routingJobID || got.Status != transit.JobStatusSucceeded {
		t.Errorf("reused %s/%s, want %s/succeeded", got.ID, got.Status, routingJobID)
	}
	var parsed map[string]any
	if err := json.Unmarshal(got.Result, &parsed); err != nil || parsed["type"] != "FeatureCollection" {
		t.Errorf("result = %s (err %v), want the stored result", got.Result, err)
	}
}

func TestFindReusableRoutingJob_quantisesTheOriginToFiveDecimalPlaces(t *testing.T) {
	repo := reuseRepo(t)
	succeededJob(t, repo, routingJobID, reuseRequest(), stamped())

	// A few centimetres away rounds to the same fifth decimal place.
	near := reuseRequest()
	near.Lat, near.Lng = 37.7912341, -122.3976538
	if _, ok := findReusable(t, repo, near); !ok {
		t.Error("an origin centimetres away was not reused")
	}

	// About ten metres away does not.
	far := reuseRequest()
	far.Lat = 37.79132
	if _, ok := findReusable(t, repo, far); ok {
		t.Error("an origin ten metres away reused another origin's result")
	}
}

func TestFindReusableRoutingJob_doesNotReuseAcrossTheKey(t *testing.T) {
	for name, vary := range map[string]func(*transit.RoutingJob){
		"graph":  func(j *transit.RoutingJob) { j.CompileJobID = reuseOtherCompileJobID },
		"budget": func(j *transit.RoutingJob) { j.BudgetMins = 90 },
		"mode":   func(j *transit.RoutingJob) { j.Mode = transit.TravelModeWalk },
	} {
		t.Run(name, func(t *testing.T) {
			repo := reuseRepo(t)
			succeededJob(t, repo, routingJobID, reuseRequest(), stamped())

			want := reuseRequest()
			vary(&want)
			if _, ok := findReusable(t, repo, want); ok {
				t.Errorf("a request differing in %s reused the earlier job", name)
			}
		})
	}
}

func TestFindReusableRoutingJob_neverCrossesAnOwnershipBoundary(t *testing.T) {
	owned := func(id string) transit.RoutingJob {
		j := reuseRequest()
		j.OwnerID = ptr(id)
		return j
	}
	cases := map[string]struct{ stored, asked transit.RoutingJob }{
		"one owner's job to another owner": {owned(routingUserID), owned(reuseOtherUserID)},
		"an owned job to an ownerless ask": {owned(routingUserID), reuseRequest()},
		"an ownerless job to an owned ask": {reuseRequest(), owned(routingUserID)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			repo := reuseRepo(t)
			succeededJob(t, repo, routingJobID, c.stored, stamped())
			if _, ok := findReusable(t, repo, c.asked); ok {
				t.Error("reused across an ownership boundary")
			}
		})
	}

	t.Run("the same owner", func(t *testing.T) {
		repo := reuseRepo(t)
		succeededJob(t, repo, routingJobID, owned(routingUserID), stamped())
		if _, ok := findReusable(t, repo, owned(routingUserID)); !ok {
			t.Error("an owner's own repeat was not reused")
		}
	})
}

func TestFindReusableRoutingJob_honoursTheWorkersServiceDateExpiry(t *testing.T) {
	repo := reuseRepo(t)
	done := stamped()
	done.ReusableUntil = time.Now().Add(-time.Minute)
	succeededJob(t, repo, routingJobID, reuseRequest(), done)

	if _, ok := findReusable(t, repo, reuseRequest()); ok {
		t.Error("a job past its reusable_until was reused; its service date has rolled over")
	}

	repo = reuseRepo(t)
	done.ReusableUntil = time.Now().Add(time.Hour)
	succeededJob(t, repo, routingJobID, reuseRequest(), done)
	if _, ok := findReusable(t, repo, reuseRequest()); !ok {
		t.Error("a job still inside its reusable_until was not reused")
	}
}

func TestFindReusableRoutingJob_requiresTheNewestKnownTileset(t *testing.T) {
	repo := reuseRepo(t)
	succeededJob(t, repo, routingJobID, reuseRequest(), stamped())

	// Any later job — any origin — that saw a newer tileset retires every
	// result cut from the older one.
	elsewhere := reuseRequest()
	elsewhere.Lat = 37.5
	succeededJob(t, repo, "00000000-0000-400b-8002-000000000002", elsewhere,
		handler.JobSucceeded{TilesetAt: reuseNewerTileset})

	if _, ok := findReusable(t, repo, reuseRequest()); ok {
		t.Error("a result cut from a superseded tileset was reused")
	}
}

func TestFindReusableRoutingJob_neverReusesAnUnstampedJob(t *testing.T) {
	repo := reuseRepo(t)
	// What an older worker, a synthetic one, or one whose /status call failed
	// reports: no tileset at all.
	succeededJob(t, repo, routingJobID, reuseRequest(), handler.JobSucceeded{})

	if _, ok := findReusable(t, repo, reuseRequest()); ok {
		t.Error("a job with no tileset stamp was reused; its freshness cannot be verified")
	}
}

func TestFindReusableRoutingJob_ignoresJobsThatHaveNotSucceeded(t *testing.T) {
	ctx := context.Background()
	repo := reuseRepo(t)

	queued := reuseRequest()
	queued.ID, queued.Status = routingJobID, transit.JobStatusQueued
	if err := repo.CreateRoutingJob(ctx, &queued); err != nil {
		t.Fatalf("CreateRoutingJob: %v", err)
	}
	failed := reuseRequest()
	failed.ID, failed.Status = "00000000-0000-400b-8002-000000000002", transit.JobStatusQueued
	if err := repo.CreateRoutingJob(ctx, &failed); err != nil {
		t.Fatalf("CreateRoutingJob: %v", err)
	}
	if err := repo.FailRoutingJob(ctx, failed.ID, "boom"); err != nil {
		t.Fatalf("FailRoutingJob: %v", err)
	}

	if got, ok := findReusable(t, repo, reuseRequest()); ok {
		t.Errorf("reused a %s job", got.Status)
	}
}

func TestFindReusableRoutingJob_prefersTheMostRecentResult(t *testing.T) {
	repo := reuseRepo(t)
	succeededJob(t, repo, routingJobID, reuseRequest(), stamped())
	later := "00000000-0000-400b-8002-000000000002"
	succeededJob(t, repo, later, reuseRequest(), stamped())

	got, ok := findReusable(t, repo, reuseRequest())
	if !ok || got.ID != later {
		t.Errorf("reused %q (ok=%v), want the later job %q", got.ID, ok, later)
	}
}

func TestFindReusableRoutingJob_isServedByItsIndexes(t *testing.T) {
	ctx := context.Background()
	_, url := freshRepo(t)
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	// An empty table always plans a sequential scan; forbidding one asks
	// whether an index *can* serve the query, which is what drifts if the
	// round(..., 5) expressions here and in 00034 stop agreeing.
	if _, err := conn.Exec(ctx, `SET enable_seqscan = off`); err != nil {
		t.Fatalf("SET: %v", err)
	}
	rows, err := conn.Query(ctx, `EXPLAIN `+postgres.FindReusableRoutingJobSQL,
		routingCompileJobID, "transit", 60, 37.79, -122.39, nil)
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan: %v", err)
		}
		plan.WriteString(line + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}

	// The reuse index can still be chosen on its leading columns alone, so
	// the origin must appear in its index condition, not just its name.
	for _, want := range []string{
		"routing_jobs_reuse_idx",
		"round((lat)::numeric, 5) =",
		"round((lng)::numeric, 5) =",
		"routing_jobs_tileset_at_idx",
	} {
		if !strings.Contains(plan.String(), want) {
			t.Errorf("plan lacks %q:\n%s", want, plan.String())
		}
	}
}
