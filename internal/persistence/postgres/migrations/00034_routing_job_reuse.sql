-- +goose Up
-- A repeated isochrone request is answered from the previous job's result
-- instead of publishing a second routing job (SPA-331).
--
-- The egress cache makes a chain cheaper; it does not free a queue slot. One
-- worker at prefetch 1 means a fully warm repeat still waits behind the whole
-- backlog. Reuse is the only way a repeat costs no slot at all.
--
-- ## The reuse key
--
-- Before minting a job, enqueueIsochrone looks for a succeeded routing job
-- with the same compile_job_id, mode, budget_mins and owner_id, and the same
-- origin quantised to five decimal places (FindReusableRoutingJob). A hit is
-- returned as it stands: no row, no publish, nothing counted as in-flight.
--
-- Origin quantisation: lat and lng compare as round(x::numeric, 5) on both
-- sides — about 1.1 m of latitude, less of longitude here. Exact float
-- equality would only ever match a replayed request; five places matches a
-- re-click on the same pin and nothing a rider would call a different place.
-- The expression is repeated in routing_jobs_reuse_idx below and in the query,
-- and the index is only used while the two agree.
--
-- Ownership: owner_id must match exactly, NULL to NULL. GET
-- /api/routing-jobs/{id} lets exactly the job's owner (or an admin, or anyone
-- for an ownerless job) read it, so a reused job is one the caller could
-- already have polled had they minted it. compile_job_id alone is not enough:
-- one compiled graph can be both a publication's pin, plotted ownerless for
-- anyone, and its owner's draft graph, plotted as owned jobs only they may
-- read.
--
-- ## Freshness: the three clocks
--
-- A reused result must go stale exactly when its polygons would.
--
-- * The graph: compile_job_id is in the key, so a recompile never reuses.
-- * The service date: transit departs at the worker's WeekdayDepartAt, a date
--   the API cannot see and must not re-implement (ADR-0001). The worker
--   reports reusable_until on success — the instant its departure clock stops
--   choosing the date it used — and a job past it is not reused. NULL means
--   no date bounds it: walk/bike/drive send Valhalla no departure.
-- * The tileset: the worker reports the tileset_at it stamped the job's
--   polygons with. A job is reused only while its stamp equals the newest
--   stamp any succeeded job carries. The API cannot ask Valhalla, so the
--   newest stamp it has seen is its best knowledge of the map: after a map
--   cycle, old results keep being served until the first job not answered by
--   reuse succeeds against the new tiles, and none are served after it. A
--   rollback to older tiles is correct but uncached for reuse, as for the
--   egress cache (00024's SPA-325 amendment), until the map moves forward.
--   A NULL stamp — an older worker, a synthetic one, or a /status outage —
--   is never reused, for SPA-325's reason: it cannot be verified.
--
-- ## Deploy order
--
-- API first, then the worker. An older worker sends neither field; its jobs
-- store NULLs and are never reused, which is today's behaviour. A newer worker
-- against an older API has its added fields ignored.

ALTER TABLE routing_jobs
    ADD COLUMN IF NOT EXISTS tileset_at timestamptz,
    ADD COLUMN IF NOT EXISTS reusable_until timestamptz;

CREATE INDEX IF NOT EXISTS routing_jobs_reuse_idx
    ON routing_jobs (compile_job_id, mode, budget_mins,
                     round(lat::numeric, 5), round(lng::numeric, 5), updated_at DESC)
    WHERE status = 'succeeded';

-- Serves max(tileset_at), read on every reuse lookup.
CREATE INDEX IF NOT EXISTS routing_jobs_tileset_at_idx
    ON routing_jobs (tileset_at)
    WHERE status = 'succeeded';

-- +goose Down
DROP INDEX IF EXISTS routing_jobs_tileset_at_idx;
DROP INDEX IF EXISTS routing_jobs_reuse_idx;
ALTER TABLE routing_jobs
    DROP COLUMN IF EXISTS reusable_until,
    DROP COLUMN IF EXISTS tileset_at;
