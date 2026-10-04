package postgres

import (
	"context"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const (
	TransitCacheServiceDateWindowDays = 7
	RoutingJobResultRetentionDays     = 30
)

// Live compile ids for the superseded predicate. The service-date window
// does not read this set, so a stale transit row on a pin is still deleted.
//
// Each of the first three arms is the latest succeeded compile for one
// non-null target, matching latestSucceededJobBySlug (GetLatestSucceededJob
// and the user-scenario and user-service reads) and LatestSucceededCompileJob:
// same kind, status succeeded, greatest created_at, tie-break id DESC. A
// newer running or failed compile is absent, and so is a job whose target
// was SET NULL.
//
// The last arm is a publication pin. That compile stays live for this
// predicate even though it is not the latest succeeded draft:
// service_publications.compile_job_id is the graph
// POST /api/services/{slug}/publication/isochrone still plots after a newer
// compile_user_service succeeds. Walk, bike and drive rows on the pin
// therefore survive this predicate.
const liveCompileIDsSQL = `
SELECT id FROM (
    SELECT DISTINCT ON (scenario_id) id
    FROM jobs
    WHERE status = '` + transit.JobStatusSucceeded + `'
      AND kind = '` + transit.JobKindCompileScenario + `'
      AND scenario_id IS NOT NULL
    ORDER BY scenario_id, created_at DESC, id DESC
) live_scenario
UNION ALL
SELECT id FROM (
    SELECT DISTINCT ON (user_scenario_id) id
    FROM jobs
    WHERE status = '` + transit.JobStatusSucceeded + `'
      AND kind = '` + transit.JobKindCompileUserScenario + `'
      AND user_scenario_id IS NOT NULL
    ORDER BY user_scenario_id, created_at DESC, id DESC
) live_user_scenario
UNION ALL
SELECT id FROM (
    SELECT DISTINCT ON (user_service_id) id
    FROM jobs
    WHERE status = '` + transit.JobStatusSucceeded + `'
      AND kind = '` + transit.JobKindCompileUserService + `'
      AND user_service_id IS NOT NULL
    ORDER BY user_service_id, created_at DESC, id DESC
) live_user_service
UNION ALL
SELECT compile_job_id FROM service_publications`

// $1 is TransitCacheServiceDateWindowDays. Walk, bike and drive store NULL
// departs_on, so the mode check keeps them out of the service-date window.
const staleTransitPred = `c.mode = 'transit'
    AND c.departs_on IS NOT NULL
    AND c.departs_on < CURRENT_DATE - $1::integer`

const supersededPred = `NOT EXISTS (SELECT 1 FROM live WHERE live.id = c.compile_job_id)`

// $1 is the service-date window, $2 is RoutingJobResultRetentionDays.
const countRetentionSQL = `
WITH live AS (
` + liveCompileIDsSQL + `
), marked AS (
    SELECT
        ` + supersededPred + ` AS superseded,
        (` + staleTransitPred + `) AS stale
    FROM isochrone_cache c
)
SELECT
    count(*) FILTER (WHERE superseded OR stale),
    count(*) FILTER (WHERE superseded),
    count(*) FILTER (WHERE stale),
    (SELECT count(*) FROM routing_jobs
      WHERE result IS NOT NULL
        AND status IN ('` + transit.JobStatusSucceeded + `', '` + transit.JobStatusFailed + `')
        AND updated_at < now() - ($2::integer * interval '1 day'))
FROM marked`

// One statement so a row matching both predicates is removed once.
const deleteIsochroneCacheSQL = `
WITH live AS (
` + liveCompileIDsSQL + `
)
DELETE FROM isochrone_cache c
WHERE ` + supersededPred + `
   OR (` + staleTransitPred + `)`

const clearRoutingJobResultSQL = `
UPDATE routing_jobs
SET result = NULL
WHERE result IS NOT NULL
  AND status IN ('` + transit.JobStatusSucceeded + `', '` + transit.JobStatusFailed + `')
  AND updated_at < now() - ($1::integer * interval '1 day')`

func (r *Repo) ApplyRetention(ctx context.Context, apply bool) (handler.RetentionReport, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return handler.RetentionReport{}, wrap("ApplyRetention begin", err)
	}
	// Dry-run returns before commit, so this rolls the read back. Apply
	// commits first; a rollback afterwards is a no-op. Shipping the endpoint
	// therefore deletes nothing until an admin calls it with apply.
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// The two cache predicates can overlap; the first count is their union.
	// Seven calendar days covers about five weekday service dates (the
	// worker's WeekdayDepartAt clock), the weekend roll onto Monday, and a
	// day of skew between the worker's local date and CURRENT_DATE.
	var report handler.RetentionReport
	report.DryRun = !apply
	if err := tx.QueryRow(ctx, countRetentionSQL,
		TransitCacheServiceDateWindowDays, RoutingJobResultRetentionDays,
	).Scan(
		&report.IsochroneCacheRows,
		&report.IsochroneCacheSuperseded,
		&report.IsochroneCacheStaleTransit,
		&report.RoutingJobResultsCleared,
	); err != nil {
		return handler.RetentionReport{}, wrap("ApplyRetention count", err)
	}
	if !apply {
		return report, nil
	}

	// Never DELETE FROM jobs. The publication foreign key is ON DELETE NO
	// ACTION (00026), so removing a pinned job fails the statement rather
	// than dropping the publication. This sweep does not delete jobs — a
	// jobs delete would also cascade into isochrone_cache and routing_jobs
	// (00014) and destroy the reuse window SPA-331 is still designing — but
	// the cache of a pin was treated as superseded, because the pin is not
	// the latest succeeded draft. The pin is in the live set, so NOT EXISTS
	// leaves that cache in place for this predicate.
	tag, err := tx.Exec(ctx, deleteIsochroneCacheSQL, TransitCacheServiceDateWindowDays)
	if err != nil {
		return handler.RetentionReport{}, wrap("ApplyRetention delete cache", err)
	}
	report.IsochroneCacheRows = tag.RowsAffected()

	// SPA-331 has not shipped and has not chosen a reuse window. It will
	// serve an identical repeat from a previous succeeded routing job
	// result, and that service-date clock is at most a few days (today, or
	// Monday when today is a weekend). Thirty days sits an order of
	// magnitude past it, so clearing a result cannot eat a transit result
	// that lookup would still serve. A non-transit repeat older than that
	// recomputes. A NULL result is a miss for that lookup.
	//
	// The row stays. updated_at is not bumped: it is when the worker wrote
	// the payload, which is the age that lookup cares about. A result
	// written recently must survive even if the row was inserted earlier.
	// queued and running are excluded so an in-flight payload is left alone.
	tag, err = tx.Exec(ctx, clearRoutingJobResultSQL, RoutingJobResultRetentionDays)
	if err != nil {
		return handler.RetentionReport{}, wrap("ApplyRetention clear routing results", err)
	}
	report.RoutingJobResultsCleared = tag.RowsAffected()

	if err := tx.Commit(ctx); err != nil {
		return handler.RetentionReport{}, wrap("ApplyRetention commit", err)
	}
	return report, nil
}
