package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/jackc/pgx/v5"
)

const (
	retentionScenarioID = "00000000-0000-4001-8333-000000000001"
	retentionSlug       = "retention-scenario"
	retentionCompileA   = "00000000-0000-400a-8333-000000000001"
	retentionCompileB   = "00000000-0000-400a-8333-000000000002"
	retentionCompileC   = "00000000-0000-400a-8333-000000000003"
	retentionRoutingOld = "00000000-0000-400b-8333-000000000001"
	retentionRoutingNew = "00000000-0000-400b-8333-000000000002"
	retentionRoutingQ   = "00000000-0000-400b-8333-000000000003"
	// Recent succeeded result on the superseded compile. Age, not liveness, decides.
	retentionRoutingRecentSup = "00000000-0000-400b-8333-000000000004"
	// Old succeeded result on the live compile.
	retentionRoutingOldLive = "00000000-0000-400b-8333-000000000005"
	retentionRoutingFailed  = "00000000-0000-400b-8333-000000000006"
	retentionRoutingRunning = "00000000-0000-400b-8333-000000000007"
	retentionPrerender      = "00000000-0000-400c-8333-000000000001"

	retentionOwnerID       = "00000000-0000-4007-8333-000000000001"
	retentionRouteID       = "00000000-0000-4002-8333-000000000001"
	retentionUserServiceID = "00000000-0000-4008-8333-000000000001"
	// Even older succeeded compile: neither the pin nor the latest draft.
	retentionSvcOld = "00000000-0000-400a-8333-000000000011"
	// Older succeeded compile pinned by service_publications.
	retentionSvcPin = "00000000-0000-400a-8333-000000000012"
	// Newer succeeded compile of the same service. Unpinned latest draft.
	retentionSvcNew         = "00000000-0000-400a-8333-000000000013"
	retentionUserScenarioID = "00000000-0000-4009-8333-000000000001"
	retentionUscnOld        = "00000000-0000-400a-8333-000000000021"
	retentionUscnNew        = "00000000-0000-400a-8333-000000000022"
	// Newer than the succeeded user-scenario compile, and failed.
	retentionUscnFail = "00000000-0000-400a-8333-000000000023"
)

func TestApplyRetention(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)
	seedRetention(t, ctx, repo, url)

	conn := retentionConn(t, ctx, url)
	jobsBefore := retentionCount(t, ctx, conn, `SELECT count(*) FROM jobs`)
	routingBefore := retentionCount(t, ctx, conn, `SELECT count(*) FROM routing_jobs`)
	cacheBefore := retentionCount(t, ctx, conn, `SELECT count(*) FROM isochrone_cache`)
	if jobsBefore != 9 || routingBefore != 7 || cacheBefore != 14 {
		t.Fatalf("fixture counts jobs=%d routing=%d cache=%d, want 9/7/14", jobsBefore, routingBefore, cacheBefore)
	}
	beforeRouting := snapshotRouting(t, ctx, conn, retentionRoutingIDs)
	for id, snap := range beforeRouting {
		if snap.isNull {
			t.Fatalf("fixture routing result %s starts null", id)
		}
	}
	prerenderBefore, ok, err := repo.GetPrerenderedIsochrone(ctx, retentionPrerender)
	if err != nil || !ok {
		t.Fatalf("prerendered before: ok=%v err=%v", ok, err)
	}

	dry, err := repo.ApplyRetention(ctx, false)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	want := retentionWant(true)
	assertRetentionReport(t, dry, want)
	assertRetentionOverlap(t, dry)
	if retentionCount(t, ctx, conn, `SELECT count(*) FROM isochrone_cache`) != cacheBefore {
		t.Fatal("dry-run deleted isochrone cache rows")
	}
	if retentionCount(t, ctx, conn, `SELECT count(*) FROM jobs`) != jobsBefore ||
		retentionCount(t, ctx, conn, `SELECT count(*) FROM routing_jobs`) != routingBefore {
		t.Fatal("dry-run changed jobs or routing_jobs row counts")
	}
	assertRoutingUntouched(t, ctx, conn, beforeRouting)
	assertCacheShape(t, ctx, conn, false)

	applied, err := repo.ApplyRetention(ctx, true)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want.DryRun = false
	assertRetentionReport(t, applied, want)
	assertRetentionOverlap(t, applied)
	if retentionCount(t, ctx, conn, `SELECT count(*) FROM jobs`) != jobsBefore {
		t.Fatalf("jobs count changed from %d", jobsBefore)
	}
	if retentionCount(t, ctx, conn, `SELECT count(*) FROM routing_jobs`) != routingBefore {
		t.Fatalf("routing_jobs count changed from %d", routingBefore)
	}
	assertCacheShape(t, ctx, conn, true)
	assertRoutingAfterApply(t, ctx, conn, beforeRouting)
	for _, id := range retentionRoutingIDs {
		if _, ok, err := repo.GetRoutingJobByID(ctx, id); err != nil || !ok {
			t.Fatalf("routing job %s missing after apply: ok=%v err=%v", id, ok, err)
		}
	}

	prerenderAfter, ok, err := repo.GetPrerenderedIsochrone(ctx, retentionPrerender)
	if err != nil || !ok {
		t.Fatalf("prerendered after: ok=%v err=%v", ok, err)
	}
	if prerenderAfter.ID != prerenderBefore.ID || !jsonEqual(t, prerenderBefore.Result, prerenderAfter.Result) {
		t.Fatalf("prerendered isochrone changed: before %s after %s", prerenderBefore.Result, prerenderAfter.Result)
	}

	again, err := repo.ApplyRetention(ctx, true)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	assertRetentionReport(t, again, handler.RetentionReport{})
}

func retentionWant(dryRun bool) handler.RetentionReport {
	// Union is 8. Six superseded rows and three stale transit rows share one
	// overlap (old compile, mode transit, departs_on outside the window), so
	// the row count is strictly less than the sum of the two predicates.
	return handler.RetentionReport{
		DryRun:                     dryRun,
		IsochroneCacheRows:         8,
		IsochroneCacheSuperseded:   6,
		IsochroneCacheStaleTransit: 3,
		RoutingJobResultsCleared:   3,
	}
}

func assertRetentionOverlap(t *testing.T, got handler.RetentionReport) {
	t.Helper()
	sum := got.IsochroneCacheSuperseded + got.IsochroneCacheStaleTransit
	if got.IsochroneCacheRows >= sum {
		t.Errorf("isochrone_cache_rows = %d, want the distinct union strictly less than superseded + stale_transit = %d", got.IsochroneCacheRows, sum)
	}
}

func assertRetentionReport(t *testing.T, got, want handler.RetentionReport) {
	t.Helper()
	if got != want {
		t.Errorf("report = %+v, want %+v", got, want)
	}
}

func seedRetention(t *testing.T, ctx context.Context, repo *postgres.Repo, url string) {
	t.Helper()
	if err := repo.CreateScenario(ctx, transit.Scenario{
		ID: retentionScenarioID, Slug: retentionSlug, Name: "Retention",
	}); err != nil {
		t.Fatalf("CreateScenario: %v", err)
	}
	if err := repo.CreateUser(ctx, account.User{
		ID: retentionOwnerID, Email: "retention@example.com", Name: "Retention",
	}, ""); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	scenarioID := retentionScenarioID
	if err := repo.CreateRoute(ctx, transit.Route{
		ID: retentionRouteID, ScenarioID: &scenarioID, Slug: "retention-route",
		Name: "Retention Route", Mode: "rail", Bidirectional: true,
		Geometry: transit.GeoLineString{
			Type: "LineString", Coordinates: [][]float64{{-122, 37}, {-121, 37}},
		},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	svcID := retentionUserServiceID
	if err := repo.CreateUserService(ctx, transit.UserService{
		ID: svcID, Slug: "retention-service", RouteID: retentionRouteID,
		OwnerID: retentionOwnerID, Name: "Retention Service",
		Stops: []transit.ServiceStopPoint{},
	}); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}
	uscnID := retentionUserScenarioID
	if err := repo.CreateUserScenario(ctx, transit.UserScenario{
		ID: uscnID, Slug: "retention-user-scenario", OwnerID: retentionOwnerID,
		Name: "Retention User Scenario",
	}); err != nil {
		t.Fatalf("CreateUserScenario: %v", err)
	}

	for _, j := range []transit.Job{
		{ID: retentionCompileA, Kind: transit.JobKindCompileScenario, Status: transit.JobStatusSucceeded, ScenarioID: &scenarioID},
		{ID: retentionCompileB, Kind: transit.JobKindCompileScenario, Status: transit.JobStatusSucceeded, ScenarioID: &scenarioID},
		{ID: retentionCompileC, Kind: transit.JobKindCompileScenario, Status: transit.JobStatusFailed, ScenarioID: &scenarioID},
		{ID: retentionSvcOld, Kind: transit.JobKindCompileUserService, Status: transit.JobStatusSucceeded, UserServiceID: &svcID},
		{ID: retentionSvcPin, Kind: transit.JobKindCompileUserService, Status: transit.JobStatusSucceeded, UserServiceID: &svcID},
		{ID: retentionSvcNew, Kind: transit.JobKindCompileUserService, Status: transit.JobStatusSucceeded, UserServiceID: &svcID},
		{ID: retentionUscnOld, Kind: transit.JobKindCompileUserScenario, Status: transit.JobStatusSucceeded, UserScenarioID: &uscnID},
		{ID: retentionUscnNew, Kind: transit.JobKindCompileUserScenario, Status: transit.JobStatusSucceeded, UserScenarioID: &uscnID},
		{ID: retentionUscnFail, Kind: transit.JobKindCompileUserScenario, Status: transit.JobStatusFailed, UserScenarioID: &uscnID},
	} {
		if err := repo.CreateJob(ctx, j); err != nil {
			t.Fatalf("CreateJob %s: %v", j.ID, err)
		}
	}

	conn := retentionConn(t, ctx, url)
	now := time.Now()
	retentionExec(t, ctx, conn,
		`UPDATE jobs SET created_at = $2 WHERE id = $1`, retentionCompileA, now.Add(-2*time.Hour))
	retentionExec(t, ctx, conn,
		`UPDATE jobs SET created_at = $2 WHERE id = $1`, retentionCompileB, now.Add(-time.Hour))
	retentionExec(t, ctx, conn,
		`UPDATE jobs SET created_at = $2 WHERE id = $1`, retentionCompileC, now)
	retentionExec(t, ctx, conn,
		`UPDATE jobs SET created_at = $2 WHERE id = $1`, retentionSvcOld, now.Add(-3*time.Hour))
	retentionExec(t, ctx, conn,
		`UPDATE jobs SET created_at = $2 WHERE id = $1`, retentionSvcPin, now.Add(-2*time.Hour))
	retentionExec(t, ctx, conn,
		`UPDATE jobs SET created_at = $2 WHERE id = $1`, retentionSvcNew, now.Add(-time.Hour))
	retentionExec(t, ctx, conn,
		`UPDATE jobs SET created_at = $2 WHERE id = $1`, retentionUscnOld, now.Add(-2*time.Hour))
	retentionExec(t, ctx, conn,
		`UPDATE jobs SET created_at = $2 WHERE id = $1`, retentionUscnNew, now.Add(-time.Hour))
	retentionExec(t, ctx, conn,
		`UPDATE jobs SET created_at = $2 WHERE id = $1`, retentionUscnFail, now)

	// The pin is an older succeeded compile, not the latest succeeded draft.
	retentionExec(t, ctx, conn,
		`INSERT INTO service_publications (user_service_id, compile_job_id, name, routes)
		 VALUES ($1, $2, $3, '[]'::jsonb)`,
		retentionUserServiceID, retentionSvcPin, "Retention Service")

	// A is superseded by B. C is newer but failed, so it must not supersede B.
	// a-overlap is both superseded and stale transit.
	insertCache(t, ctx, conn, retentionCompileA, "a-walk", "walk", "NULL", nil)
	insertCache(t, ctx, conn, retentionCompileA, "a-transit", "transit", "CURRENT_DATE", 30)
	insertCache(t, ctx, conn, retentionCompileB, "b-walk", "walk", "NULL", nil)
	insertCache(t, ctx, conn, retentionCompileB, "b-today", "transit", "CURRENT_DATE", 30)
	insertCache(t, ctx, conn, retentionCompileB, "b-yesterday", "transit", "CURRENT_DATE - 1", 30)
	stale := fmt.Sprintf("CURRENT_DATE - %d", postgres.TransitCacheServiceDateWindowDays+1)
	insertCache(t, ctx, conn, retentionCompileA, "a-overlap", "transit", stale, 30)
	insertCache(t, ctx, conn, retentionCompileB, "b-stale", "transit", stale, 30)

	// Pin walk stays. Stale transit on the pin is outside the service-date window and is still deleted.
	insertCache(t, ctx, conn, retentionSvcPin, "pin-walk", "walk", "NULL", nil)
	insertCache(t, ctx, conn, retentionSvcPin, "pin-stale", "transit", stale, 30)
	insertCache(t, ctx, conn, retentionSvcNew, "svc-new-walk", "walk", "NULL", nil)
	insertCache(t, ctx, conn, retentionSvcOld, "svc-old-walk", "walk", "NULL", nil)
	insertCache(t, ctx, conn, retentionUscnOld, "uscn-old-walk", "walk", "NULL", nil)
	insertCache(t, ctx, conn, retentionUscnNew, "uscn-new-walk", "walk", "NULL", nil)
	insertCache(t, ctx, conn, retentionUscnFail, "uscn-fail-walk", "walk", "NULL", nil)

	for _, j := range []transit.RoutingJob{
		{ID: retentionRoutingOld, Status: transit.JobStatusSucceeded, CompileJobID: retentionCompileA, Lat: 37.7, Lng: -122.4, BudgetMins: 30, Mode: transit.TravelModeWalk},
		{ID: retentionRoutingNew, Status: transit.JobStatusSucceeded, CompileJobID: retentionCompileB, Lat: 37.7, Lng: -122.4, BudgetMins: 30, Mode: transit.TravelModeWalk},
		{ID: retentionRoutingQ, Status: transit.JobStatusQueued, CompileJobID: retentionCompileB, Lat: 37.7, Lng: -122.4, BudgetMins: 30, Mode: transit.TravelModeWalk},
		{ID: retentionRoutingRecentSup, Status: transit.JobStatusSucceeded, CompileJobID: retentionCompileA, Lat: 37.7, Lng: -122.4, BudgetMins: 30, Mode: transit.TravelModeWalk},
		{ID: retentionRoutingOldLive, Status: transit.JobStatusSucceeded, CompileJobID: retentionCompileB, Lat: 37.7, Lng: -122.4, BudgetMins: 30, Mode: transit.TravelModeWalk},
		{ID: retentionRoutingFailed, Status: transit.JobStatusFailed, CompileJobID: retentionCompileB, Lat: 37.7, Lng: -122.4, BudgetMins: 30, Mode: transit.TravelModeWalk},
		{ID: retentionRoutingRunning, Status: transit.JobStatusRunning, CompileJobID: retentionCompileB, Lat: 37.7, Lng: -122.4, BudgetMins: 30, Mode: transit.TravelModeWalk},
	} {
		if err := repo.CreateRoutingJob(ctx, &j); err != nil {
			t.Fatalf("CreateRoutingJob %s: %v", j.ID, err)
		}
	}
	aged := now.Add(-time.Hour * 24 * time.Duration(postgres.RoutingJobResultRetentionDays+1))
	retentionExec(t, ctx, conn,
		`UPDATE routing_jobs SET result = $2::jsonb, updated_at = $3 WHERE id = $1`,
		retentionRoutingOld, `{"marker":"old"}`, aged)
	retentionExec(t, ctx, conn,
		`UPDATE routing_jobs SET result = $2::jsonb WHERE id = $1`,
		retentionRoutingNew, `{"marker":"new"}`)
	retentionExec(t, ctx, conn,
		`UPDATE routing_jobs SET result = $2::jsonb, updated_at = $3 WHERE id = $1`,
		retentionRoutingQ, `{"marker":"queued"}`, aged)
	retentionExec(t, ctx, conn,
		`UPDATE routing_jobs SET result = $2::jsonb WHERE id = $1`,
		retentionRoutingRecentSup, `{"marker":"recent-superseded"}`)
	retentionExec(t, ctx, conn,
		`UPDATE routing_jobs SET result = $2::jsonb, updated_at = $3 WHERE id = $1`,
		retentionRoutingOldLive, `{"marker":"old-live"}`, aged)
	retentionExec(t, ctx, conn,
		`UPDATE routing_jobs SET result = $2::jsonb, updated_at = $3 WHERE id = $1`,
		retentionRoutingFailed, `{"marker":"failed"}`, aged)
	retentionExec(t, ctx, conn,
		`UPDATE routing_jobs SET result = $2::jsonb, updated_at = $3 WHERE id = $1`,
		retentionRoutingRunning, `{"marker":"running"}`, aged)

	entry := transit.PrerenderedIsochrone{
		ID: retentionPrerender, ScenarioSlug: retentionSlug, Label: "kept",
		Lat: 37.7, Lng: -122.4, BudgetMins: 30, Mode: transit.TravelModeWalk,
		Result: json.RawMessage(`{"marker":"prerendered"}`),
	}
	if err := repo.CreatePrerenderedIsochrone(ctx, &entry); err != nil {
		t.Fatalf("CreatePrerenderedIsochrone: %v", err)
	}
}

func insertCache(t *testing.T, ctx context.Context, conn *pgx.Conn, jobID, station, mode, departsExpr string, budget any) {
	t.Helper()
	q := `INSERT INTO isochrone_cache
		(compile_job_id, station_slug, mode, contour_mins, geometry, departs_on, budget_mins)
		VALUES ($1, $2, $3, 30, '{"type":"Polygon"}', ` + departsExpr + `, $4)`
	if _, err := conn.Exec(ctx, q, jobID, station, mode, budget); err != nil {
		t.Fatalf("insert cache %s/%s: %v", jobID, station, err)
	}
}

func retentionConn(t *testing.T, ctx context.Context, url string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	return conn
}

func retentionExec(t *testing.T, ctx context.Context, conn *pgx.Conn, sql string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec %s: %v", sql, err)
	}
}

func retentionCount(t *testing.T, ctx context.Context, conn *pgx.Conn, sql string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(ctx, sql).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", sql, err)
	}
	return n
}

var retentionRoutingIDs = []string{
	retentionRoutingOld,
	retentionRoutingNew,
	retentionRoutingQ,
	retentionRoutingRecentSup,
	retentionRoutingOldLive,
	retentionRoutingFailed,
	retentionRoutingRunning,
}

// Cleared results are aged succeeded or failed rows. A recent result on a
// superseded compile stays, and so do aged queued and running rows.
var retentionRoutingCleared = map[string]bool{
	retentionRoutingOld:     true,
	retentionRoutingOldLive: true,
	retentionRoutingFailed:  true,
}

type routingSnap struct {
	updated time.Time
	isNull  bool
}

func snapshotRouting(t *testing.T, ctx context.Context, conn *pgx.Conn, ids []string) map[string]routingSnap {
	t.Helper()
	out := make(map[string]routingSnap, len(ids))
	for _, id := range ids {
		updated, isNull := retentionRouting(t, ctx, conn, id)
		out[id] = routingSnap{updated: updated, isNull: isNull}
	}
	return out
}

func assertRoutingUntouched(t *testing.T, ctx context.Context, conn *pgx.Conn, before map[string]routingSnap) {
	t.Helper()
	for _, id := range retentionRoutingIDs {
		updated, isNull := retentionRouting(t, ctx, conn, id)
		snap := before[id]
		if isNull || !updated.Equal(snap.updated) {
			t.Errorf("dry-run changed routing %s (null=%v updated %s -> %s)", id, isNull, snap.updated, updated)
		}
	}
}

func assertRoutingAfterApply(t *testing.T, ctx context.Context, conn *pgx.Conn, before map[string]routingSnap) {
	t.Helper()
	for _, id := range retentionRoutingIDs {
		updated, isNull := retentionRouting(t, ctx, conn, id)
		snap := before[id]
		if !updated.Equal(snap.updated) {
			t.Errorf("%s updated_at %s, want the timestamp from before apply %s", id, updated, snap.updated)
		}
		if isNull != retentionRoutingCleared[id] {
			t.Errorf("%s result null=%v, want null=%v", id, isNull, retentionRoutingCleared[id])
		}
	}
}

func retentionRouting(t *testing.T, ctx context.Context, conn *pgx.Conn, id string) (time.Time, bool) {
	t.Helper()
	var updated time.Time
	var isNull bool
	err := conn.QueryRow(ctx,
		`SELECT updated_at, result IS NULL FROM routing_jobs WHERE id = $1`, id).Scan(&updated, &isNull)
	if err != nil {
		t.Fatalf("routing %s: %v", id, err)
	}
	return updated, isNull
}

func assertCacheShape(t *testing.T, ctx context.Context, conn *pgx.Conn, applied bool) {
	t.Helper()
	rows, err := conn.Query(ctx, `SELECT compile_job_id::text, station_slug FROM isochrone_cache`)
	if err != nil {
		t.Fatalf("list cache: %v", err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var jobID, station string
		if err := rows.Scan(&jobID, &station); err != nil {
			t.Fatalf("scan cache: %v", err)
		}
		got[jobID+"/"+station] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("cache rows: %v", err)
	}
	kept := []string{
		retentionCompileB + "/b-walk",
		retentionCompileB + "/b-today",
		retentionCompileB + "/b-yesterday",
		retentionSvcPin + "/pin-walk",
		retentionSvcNew + "/svc-new-walk",
		retentionUscnNew + "/uscn-new-walk",
	}
	doomed := []string{
		retentionCompileA + "/a-walk",
		retentionCompileA + "/a-transit",
		retentionCompileA + "/a-overlap",
		retentionCompileB + "/b-stale",
		retentionSvcOld + "/svc-old-walk",
		retentionSvcPin + "/pin-stale",
		retentionUscnOld + "/uscn-old-walk",
		retentionUscnFail + "/uscn-fail-walk",
	}
	if applied {
		if len(got) != len(kept) {
			t.Fatalf("cache after apply = %v, want %v", got, kept)
		}
		for _, k := range kept {
			if !got[k] {
				t.Errorf("apply removed %s", k)
			}
		}
		for _, k := range doomed {
			if got[k] {
				t.Errorf("apply kept %s", k)
			}
		}
		return
	}
	for _, k := range append(kept, doomed...) {
		if !got[k] {
			t.Errorf("dry-run missing %s", k)
		}
	}
}
