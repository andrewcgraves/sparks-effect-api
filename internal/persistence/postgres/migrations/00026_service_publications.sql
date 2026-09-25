-- +goose Up
-- A publication is the frozen public copy of an authored service (SPA-355).
--
-- Publishing does not compile. It pins the owner's latest succeeded
-- compile_user_service job, provided that job is still current against the
-- draft, and in the same transaction copies the draft's name, subtext and
-- description plus the routes the pinned graph's edges name. Published means
-- a row exists. Unpublishing deletes the row. Every service that already
-- exists stays unpublished: this migration does not backfill.
--
-- ## Why a table, not columns on user_services
--
-- One row per published service, keyed on the service id, so the pin, the
-- prose and the route copies cannot drift out of step with each other. A
-- later public read selects from a table that holds nothing unpublished, and
-- never from the draft. See ADR-0005.
--
-- ## The pin outlives retention
--
-- compile_job_id references jobs(id) with the default ON DELETE NO ACTION.
-- A retention DELETE of a pinned job must fail rather than silently drop the
-- publication. The clause is omitted on purpose: writing CASCADE or SET NULL
-- would be the bug this foreign key exists to prevent.
--
-- Deleting the service still removes its jobs and its publication together.
-- Both children cascade from user_services, and PostgreSQL checks NO ACTION
-- at the end of the statement, so one DELETE FROM user_services can drop the
-- job and the publication in either order.
--
-- ## routes is a frozen copy
--
-- jsonb of the full route rows the pinned graph's edges name, geometry
-- included. A later edit of routes, or a re-point of user_services.route_id,
-- must not change what was published. An empty edge set stores [] — the
-- CHECK rejects SQL NULL and JSON null alike.
--
-- ## Re-running
--
-- CREATE TABLE IF NOT EXISTS for 00025's reason: a schema change re-run
-- against data it already wrote must not fail on "already exists". It is also
-- what lets the package's migration-rewind tests unrecord this version with a
-- bare DELETE and bring the database forward again.

CREATE TABLE IF NOT EXISTS service_publications (
    user_service_id uuid PRIMARY KEY,
    compile_job_id  uuid NOT NULL,
    name            text NOT NULL,
    subtext         text NOT NULL DEFAULT '',
    description     text NOT NULL DEFAULT '',
    routes          jsonb NOT NULL,
    published_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT service_publications_user_service_id_fkey
        FOREIGN KEY (user_service_id) REFERENCES user_services (id) ON DELETE CASCADE,
    CONSTRAINT service_publications_compile_job_id_fkey
        FOREIGN KEY (compile_job_id) REFERENCES jobs (id),
    CONSTRAINT service_publications_routes_is_array
        CHECK (jsonb_typeof(routes) = 'array')
);

-- +goose Down
DROP TABLE IF EXISTS service_publications;
