-- +goose Up
-- The one route_id foreign key without an index (SPA-481).
--
-- CountRouteDependents counts services, user_services and segments by
-- route_id. user_services (00005) and segments (00011) each index that
-- column; services (00001) never did, because nothing read it by route until
-- the owner's route reads started reporting dependents, once per route in
-- GET /api/me/routes. The table is small, so the seq scan was never slow —
-- but a per-route count on a list endpoint is exactly the query that should
-- not depend on the table staying small.
--
-- ## Re-running
--
-- IF NOT EXISTS for 00022's reason: a re-run against a database that already
-- has the index must not fail on "already exists".

CREATE INDEX IF NOT EXISTS services_route_id_idx ON services (route_id);

-- +goose Down
DROP INDEX IF EXISTS services_route_id_idx;
