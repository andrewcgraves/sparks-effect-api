-- +goose Up
-- Adds 'transit' — walk plus scheduled local transit — to the travel modes an
-- isochrone may be requested in, and makes the database state what that set is
-- (SPA-248, following SPA-246's design note).
--
-- ## This establishes the constraint rather than widening it
--
-- SPA-248 was written expecting to find a CHECK on the mode column and add one
-- value to it. There was none: 00014 and 00019 both declared `mode text NOT
-- NULL` and left the set entirely to transit.TravelMode.Valid in Go. So the
-- constraint is created here, with the new mode already in it.
--
-- Creating it is the ticket rather than a detour from it. transit.TravelMode
-- claims to be "the single definition of the set, so the request validator, the
-- queue message, and the database cannot drift apart on what a mode is", and
-- the database was not holding up its end of that: any string at all was
-- storable, and a mode the API would refuse could still arrive by hand or from
-- a second writer. Adding a fourth value to a set nothing enforces would have
-- widened a guarantee that did not exist.
--
-- ## Why validating existing rows is safe
--
-- Postgres checks every existing row when a CHECK is added, so a single junk
-- mode anywhere would fail this migration and, since migrations run on boot,
-- the deploy with it. Each column has exactly one writer and each validates
-- first: routing_jobs.mode only through CreateRoutingJob, behind
-- handler.validateIsochroneParams on all three isochrone endpoints, and
-- prerendered_isochrones.mode only through CreatePrerenderedIsochrone, behind
-- the same validator for the admin POST and behind readPrerenderedSeedFile for
-- the seeder. Both call TravelMode.Valid, so every stored mode is one this
-- constraint already accepts. NOT VALID was rejected for that reason: it would
-- buy nothing but leave the claim above still untrue of the rows on disk.
--
-- ## What is deliberately left unconstrained
--
-- isochrone_cache.mode (00014) is written only by the routing worker, from the
-- other repository. Its spelling of a mode is not checkable from here — there
-- is no compiler spanning the two and no migration of ours it deploys with — so
-- a constraint on it would be this repository betting on another's serialized
-- form and failing the worker's inserts at runtime if it lost. The bet is not
-- worth it: that table is a cache, and a wrong mode in a cache key produces a
-- miss, not a wrong isochrone.
--
-- routes.mode (00001) is not a travel mode at all. It is what a route *is*
-- ("rail"), not how a person reaches it, and shares only a column name. It must
-- not be folded into this constraint.
--
-- ## DROP then ADD
--
-- Postgres has no ADD CONSTRAINT IF NOT EXISTS, and a schema change re-run
-- against data it already wrote must not fail on "constraint already exists"
-- (00020's reason for IF NOT EXISTS). Dropping first makes this re-runnable,
-- and makes the next mode a two-line edit in a new migration rather than a
-- hand-written ALTER against whatever the previous one happened to name.

ALTER TABLE routing_jobs
    DROP CONSTRAINT IF EXISTS routing_jobs_mode_valid,
    ADD CONSTRAINT routing_jobs_mode_valid
        CHECK (mode IN ('walk', 'bike', 'drive', 'transit'));

ALTER TABLE prerendered_isochrones
    DROP CONSTRAINT IF EXISTS prerendered_isochrones_mode_valid,
    ADD CONSTRAINT prerendered_isochrones_mode_valid
        CHECK (mode IN ('walk', 'bike', 'drive', 'transit'));

-- +goose Down
ALTER TABLE routing_jobs
    DROP CONSTRAINT IF EXISTS routing_jobs_mode_valid;

ALTER TABLE prerendered_isochrones
    DROP CONSTRAINT IF EXISTS prerendered_isochrones_mode_valid;
