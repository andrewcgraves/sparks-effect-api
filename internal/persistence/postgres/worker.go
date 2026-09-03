package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

var _ handler.WorkerStore = (*Repo)(nil)

// MarkRoutingJobRunning moves a job to running.
//
// It is the one transition allowed to be a no-op: a job redelivered after a
// crash is already running, and refusing to re-mark it would fail work that
// is otherwise recoverable. Terminal statuses are excluded so a late
// redelivery cannot drag a finished job backwards; that case, and a missing
// row, both report handler.ErrJobNotFound.
func (r *Repo) MarkRoutingJobRunning(ctx context.Context, id string) error {
	return r.execRoutingJob(ctx, "MarkRoutingJobRunning", id,
		`UPDATE routing_jobs
		    SET status = $2, updated_at = now()
		  WHERE id = $1
		    AND status NOT IN ($3, $4)`,
		id, transit.JobStatusRunning, transit.JobStatusSucceeded, transit.JobStatusFailed)
}

// SucceedRoutingJob records the computed isochrone and completes the job.
//
// An unconditional overwrite keyed by id: recomputing over an immutable graph
// produces the same answer, so a second write is indistinguishable from the
// first.
func (r *Repo) SucceedRoutingJob(ctx context.Context, id string, result json.RawMessage) error {
	return r.execRoutingJob(ctx, "SucceedRoutingJob", id,
		`UPDATE routing_jobs
		    SET status = $2, result = $3, error = '', updated_at = now()
		  WHERE id = $1`,
		id, transit.JobStatusSucceeded, []byte(result))
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

// GetIsochroneCache looks every key up in one round trip.
//
// The keys arrive as parallel arrays unnested into a join, rather than as a
// statement built per call: a chain over a large scenario asks about as many
// stations as it found reachable, and a query whose text grows with that
// would defeat the statement cache on every differently sized request.
//
// The keys it returns are the caller's own, echoed back out of the join rather
// than read off the row. Postgres normalises a uuid on the way in, so a caller
// that spelled one with different casing would otherwise get back a key it
// could not find in its own map — a hit that reads as a miss.
//
// departs_on is compared with IS NOT DISTINCT FROM so a missing date (walk /
// bike / drive, or a worker that has not started sending one) matches the
// NULL row, while two transit dates stay two rows (SPA-269).
func (r *Repo) GetIsochroneCache(ctx context.Context, keys []handler.IsochroneKey) (map[handler.IsochroneKey]json.RawMessage, error) {
	found := make(map[handler.IsochroneKey]json.RawMessage, len(keys))
	if len(keys) == 0 {
		return found, nil
	}

	compileJobIDs := make([]string, len(keys))
	slugs := make([]string, len(keys))
	modes := make([]string, len(keys))
	contours := make([]int32, len(keys))
	departsOn := make([]string, len(keys))
	for i, k := range keys {
		compileJobIDs[i] = k.CompileJobID
		slugs[i] = k.StationSlug
		modes[i] = k.Mode
		contours[i] = int32(k.ContourMins)
		departsOn[i] = k.DepartsOn
	}

	rows, err := r.pool.Query(ctx,
		`SELECT wanted.compile_job_id, wanted.station_slug, wanted.mode, wanted.contour_mins,
		        wanted.departs_on, c.geometry
		   FROM unnest($1::text[], $2::text[], $3::text[], $4::int[], $5::text[])
		        AS wanted (compile_job_id, station_slug, mode, contour_mins, departs_on)
		   JOIN isochrone_cache c
		     ON c.compile_job_id = wanted.compile_job_id::uuid
		    AND c.station_slug   = wanted.station_slug
		    AND c.mode           = wanted.mode
		    AND c.contour_mins   = wanted.contour_mins
		    AND c.departs_on IS NOT DISTINCT FROM NULLIF(wanted.departs_on, '')::date`,
		compileJobIDs, slugs, modes, contours, departsOn)
	if err != nil {
		return nil, wrap("GetIsochroneCache", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			k    handler.IsochroneKey
			geom []byte
		)
		if err := rows.Scan(&k.CompileJobID, &k.StationSlug, &k.Mode, &k.ContourMins, &k.DepartsOn, &geom); err != nil {
			return nil, wrap("GetIsochroneCache scan", err)
		}
		found[k] = json.RawMessage(geom)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("GetIsochroneCache", err)
	}
	return found, nil
}

// PutIsochroneCache writes the batch in one round trip.
//
// DO NOTHING rather than DO UPDATE: a row is only ever written after a miss,
// so a conflict means another worker chained over the same graph concurrently
// and computed the same inputs into the same polygon.
//
// An empty DepartsOn is stored NULL. Walk/bike/drive have no service date;
// transit sends YYYY-MM-DD so two dates stay two rows (SPA-269). ON CONFLICT
// targets the five-column unique (isochrone_cache_key) 00024 introduced.
func (r *Repo) PutIsochroneCache(ctx context.Context, entries []handler.CachedIsochrone) error {
	if len(entries) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, e := range entries {
		var tilesetAt *time.Time
		if !e.TilesetAt.IsZero() {
			at := e.TilesetAt
			tilesetAt = &at
		}
		batch.Queue(
			`INSERT INTO isochrone_cache
			     (compile_job_id, station_slug, mode, contour_mins, geometry, tileset_at, departs_on)
			  VALUES ($1::uuid, $2, $3, $4, $5, $6, NULLIF($7, '')::date)
			  ON CONFLICT ON CONSTRAINT isochrone_cache_key DO NOTHING`,
			e.Key.CompileJobID, e.Key.StationSlug, e.Key.Mode, e.Key.ContourMins,
			[]byte(e.Geometry), tilesetAt, e.Key.DepartsOn)
	}

	if err := r.pool.SendBatch(ctx, batch).Close(); err != nil {
		return wrap("PutIsochroneCache", err)
	}
	return nil
}
