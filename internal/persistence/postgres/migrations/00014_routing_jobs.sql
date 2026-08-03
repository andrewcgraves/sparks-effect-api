-- +goose Up
-- The isochrone stops being computed in this process and becomes queued work
-- (SPA-182).
--
-- Valhalla runs ClusterIP-only on the home cluster with no ingress, so the API
-- cannot reach it and the queue is the only transport between them. The API's
-- part is now to resolve a request down to one immutable compiled graph, insert
-- the row below, publish it, and let the caller poll.
--
-- ## Why two tables here rather than in the worker's own repository
--
-- Both want real foreign keys — into `users` and into `jobs` — and a foreign
-- key cannot cross a schema owned by a different migration sequence. Keeping
-- them here buys referential integrity at the cost of the two repositories
-- sharing a schema; the alternative buys separation at the cost of orphan rows
-- nothing cleans up. `routing_jobs` is co-owned by construction: this repo
-- inserts and polls it, the worker transitions it and writes its result.

CREATE TABLE routing_jobs (
    id     uuid PRIMARY KEY,
    -- Reuses the jobs table's queued/running/succeeded/failed vocabulary
    -- rather than minting a second one (see transit.RoutingJob).
    status text NOT NULL,
    -- The compiled graph this isochrone is plotted over, named by the job that
    -- produced it — a graph's identity is its compile job (SPA-181). NOT NULL:
    -- a routing job with no graph is not a request anyone can answer. CASCADE
    -- because such a job is unanswerable garbage once the graph is gone.
    compile_job_id uuid NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    -- NULL means the public seeded isochrone, which nobody authenticates to
    -- request. That is deliberately readable by anyone holding the id, so this
    -- FK CASCADEs rather than SET NULL like jobs.owner_id does: setting it null
    -- on account deletion would silently widen a private job to a public one.
    owner_id    uuid REFERENCES users (id) ON DELETE CASCADE,
    lat         double precision NOT NULL,
    lng         double precision NOT NULL,
    budget_mins integer NOT NULL,
    -- The domain's own vocabulary: walk / bike / drive. "Costing" is Valhalla's
    -- word for the same concept and stays at the worker's client boundary; a
    -- second column would be a thing that must agree with the first forever.
    mode text NOT NULL,
    -- Written by the worker, opaque to this repository — the API does not
    -- produce or interpret an isochrone any more.
    result     jsonb,
    error      text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- The worker claims queued work by status; nothing else scans this table.
CREATE INDEX routing_jobs_status_idx ON routing_jobs (status);

-- Valhalla's per-station isochrone polygons, cached so the same station at the
-- same budget is not re-routed for every request. Written only by the worker.
--
-- The key is what makes a cached polygon reusable: the compiled graph (whose
-- node positions determine where the polygon is centred), the station, the
-- travel mode, and the contour. Two requests agreeing on all four get the same
-- answer, whatever else differs between them.
CREATE TABLE isochrone_cache (
    compile_job_id uuid NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    station_slug   text NOT NULL,
    mode           text NOT NULL,
    contour_mins   integer NOT NULL,
    -- The GeoJSON geometry Valhalla returned for this contour.
    geometry jsonb NOT NULL,
    -- Diagnostic only, deliberately not part of the key: when the routing
    -- tiles the polygon was cut from were built. It answers "is this cached
    -- shape from before the OSM refresh?" after the fact. Keying on it would
    -- silently invalidate the whole cache on every tile rebuild, which is a
    -- decision to take explicitly rather than by accident.
    tileset_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (compile_job_id, station_slug, mode, contour_mins)
);

-- +goose Down
DROP TABLE IF EXISTS isochrone_cache;
DROP TABLE IF EXISTS routing_jobs;
