package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/jackc/pgx/v5"
)

var (
	_ handler.WorkerStore       = (*Repo)(nil)
	_ handler.RoutingQueueStore = (*Repo)(nil)
)

func (r *Repo) MarkRoutingJobRunning(ctx context.Context, id string) error {
	return r.execRoutingJob(ctx, "MarkRoutingJobRunning", id,
		`UPDATE routing_jobs
		    SET status = $2, updated_at = now()
		  WHERE id = $1
		    AND status NOT IN ($3, $4)`,
		id, transit.JobStatusRunning, transit.JobStatusSucceeded, transit.JobStatusFailed)
}

func (r *Repo) SucceedRoutingJob(ctx context.Context, id string, done handler.JobSucceededBody) error {
	return r.execRoutingJob(ctx, "SucceedRoutingJob", id,
		`UPDATE routing_jobs
		    SET status = $2, result = $3, error = '', updated_at = now(),
		        tileset_at = $4, reusable_until = $5
		  WHERE id = $1`,
		id, transit.JobStatusSucceeded, []byte(done.Result),
		nullTime(done.TilesetAt), nullTime(done.ReusableUntil))
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (r *Repo) RoutingQueueSnapshot(ctx context.Context, within time.Duration) (handler.RoutingQueue, error) {
	var q handler.RoutingQueue
	var oldest *time.Time
	err := r.pool.QueryRow(ctx,
		`SELECT count(*), min(created_at) FILTER (WHERE status = $2)
		   FROM routing_jobs
		  WHERE status = ANY($1) AND created_at > $3`,
		[]string{transit.JobStatusQueued, transit.JobStatusRunning},
		transit.JobStatusQueued,
		time.Now().Add(-within),
	).Scan(&q.InFlight, &oldest)
	if err != nil {
		return handler.RoutingQueue{}, wrap("RoutingQueueSnapshot", err)
	}
	if oldest != nil {
		q.OldestQueuedAt = *oldest
	}
	return q, nil
}

func (r *Repo) execRoutingJob(ctx context.Context, op, id, sql string, args ...any) error {
	tag, err := r.pool.Exec(ctx, sql, args...)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", handler.ErrJobNotFound, id)
	}
	return nil
}

func (r *Repo) GetIsochroneCache(ctx context.Context, keys []handler.IsochroneKey) (map[handler.IsochroneKey]handler.CachedIsochrone, error) {
	found := make(map[handler.IsochroneKey]handler.CachedIsochrone, len(keys))
	if len(keys) == 0 {
		return found, nil
	}

	compileJobIDs := make([]string, len(keys))
	slugs := make([]string, len(keys))
	modes := make([]string, len(keys))
	contours := make([]int32, len(keys))
	departsOn := make([]string, len(keys))
	budgets := make([]int32, len(keys))
	for i, k := range keys {
		compileJobIDs[i] = k.CompileJobID
		slugs[i] = k.StationSlug
		modes[i] = k.Mode
		contours[i] = int32(k.ContourMins)
		departsOn[i] = k.DepartsOn
		budgets[i] = int32(k.BudgetMins)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT wanted.compile_job_id, wanted.station_slug, wanted.mode, wanted.contour_mins,
		        wanted.departs_on, wanted.budget_mins, c.geometry, c.tileset_at
		   FROM unnest($1::text[], $2::text[], $3::text[], $4::int[], $5::text[], $6::int[])
		        AS wanted (compile_job_id, station_slug, mode, contour_mins, departs_on, budget_mins)
		   JOIN isochrone_cache c
		     ON c.compile_job_id = wanted.compile_job_id::uuid
		    AND c.station_slug   = wanted.station_slug
		    AND c.mode           = wanted.mode
		    AND c.contour_mins   = wanted.contour_mins
		    AND c.departs_on IS NOT DISTINCT FROM NULLIF(wanted.departs_on, '')::date
		    AND c.budget_mins IS NOT DISTINCT FROM NULLIF(wanted.budget_mins, 0)`,
		compileJobIDs, slugs, modes, contours, departsOn, budgets)
	if err != nil {
		return nil, wrap("GetIsochroneCache", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			k         handler.IsochroneKey
			geom      []byte
			tilesetAt *time.Time
		)
		if err := rows.Scan(&k.CompileJobID, &k.StationSlug, &k.Mode, &k.ContourMins, &k.DepartsOn, &k.BudgetMins, &geom, &tilesetAt); err != nil {
			return nil, wrap("GetIsochroneCache scan", err)
		}
		row := handler.CachedIsochrone{Key: k, Geometry: json.RawMessage(geom)}
		if tilesetAt != nil {
			row.TilesetAt = tilesetAt.UTC()
		}
		found[k] = row
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("GetIsochroneCache", err)
	}
	return found, nil
}

func (r *Repo) PutIsochroneCache(ctx context.Context, entries []handler.CachedIsochrone) error {
	if len(entries) == 0 {
		return nil
	}

	// A conflicting row is replaced only by a usable polygon, and only when
	// the stored one is unusable or was cut from an older or unknown tileset
	// (SPA-328). Under DO NOTHING a row the worker rejects on read was
	// recomputed and then dropped, on every request, forever; an unconditional
	// DO UPDATE would let a synthetic or stale-tileset worker overwrite a good
	// row instead. A conflict that fails the guard is a no-op, not an error, so
	// it still cannot fail a job.
	//
	// DO UPDATE can deadlock two concurrent batches touching the same keys in
	// different orders, which DO NOTHING could not. One worker replica at
	// prefetch 1 means there is only ever one writer.
	//
	// The batch runs as one implicit transaction, so one rejected entry loses
	// every polygon in the put, and that is kept on purpose (SPA-332). Since a
	// guarded conflict is a no-op, only an entry the schema refuses can be
	// rejected: a bad uuid or date, an unknown mode, or a compile job that no
	// longer exists. Every entry in a put shares one compile job and mode, so
	// such an entry makes the put suspect as a whole. A partial
	// write would leave a job's egress half cached for reasons nothing reports,
	// while losing the put costs one recompute on the next request.
	batch := &pgx.Batch{}
	for _, e := range entries {
		batch.Queue(putIsochroneCacheSQL,
			e.Key.CompileJobID, e.Key.StationSlug, e.Key.Mode, e.Key.ContourMins,
			[]byte(e.Geometry), nullTime(e.TilesetAt), e.Key.DepartsOn, e.Key.BudgetMins)
	}

	if err := r.pool.SendBatch(ctx, batch).Close(); err != nil {
		return wrap("PutIsochroneCache", err)
	}
	return nil
}

var putIsochroneCacheSQL = `INSERT INTO isochrone_cache
     (compile_job_id, station_slug, mode, contour_mins, geometry, tileset_at, departs_on, budget_mins)
  VALUES ($1::uuid, $2, $3, $4, $5, $6, NULLIF($7, '')::date, NULLIF($8::int, 0))
  ON CONFLICT ON CONSTRAINT isochrone_cache_key DO UPDATE
     SET geometry = excluded.geometry, tileset_at = excluded.tileset_at
   WHERE ` + usableGeometrySQL("excluded.geometry") + `
     AND (NOT ` + usableGeometrySQL("isochrone_cache.geometry") + `
          OR (excluded.tileset_at IS NOT NULL
              AND (isochrone_cache.tileset_at IS NULL
                   OR excluded.tileset_at > isochrone_cache.tileset_at)))`

func usableGeometrySQL(col string) string {
	// The worker's own test for a row it will serve: the geometry unmarshals
	// into valhalla.IsochroneResponse ({type string, features
	// []json.RawMessage}) and isochrone.usable finds at least one feature.
	// Nothing fails if the two drift, so change both together. Every term is
	// NULL-safe so that NOT of it is never NULL: a missing key must read as
	// unusable, not unknown. encoding/json matches keys case-insensitively and
	// this does not, which only errs toward replacing a row.
	return strings.NewReplacer("$g", col).Replace(
		`(COALESCE(jsonb_typeof($g->'features'), '') = 'array'
		  AND $g->'features' <> '[]'::jsonb
		  AND COALESCE(jsonb_typeof($g->'type'), 'null') IN ('string', 'null'))`)
}
