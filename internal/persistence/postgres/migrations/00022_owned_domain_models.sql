-- +goose Up
-- Ownership for the seeded domain models.
--
-- Until now the seeded models were platform data: authored as embedded YAML,
-- written once by SeedIfEmpty, and mutable only by editing the repository. This
-- gives them the same ownership the user-authored models (00005, 00006) already
-- have, so a person can introduce a scenario of their own — with real routes,
-- stations, segments, and services — and keep editing it.
--
-- ## The invariant this schema serves
--
-- A scenario and all of its children share one owner. A curated scenario has
-- curated children; an owned scenario's routes, stations, services, and
-- segments are owned by the same user. The sole exception is a standalone route
-- (routes.scenario_id IS NULL), which carries its own owner.
--
-- The invariant is enforced by the handlers, not here: a CHECK cannot express
-- "owner matches the parent scenario's owner" without a trigger or a
-- denormalized column, and neither earns its keep for a rule with one
-- enforcement point. What the schema does provide is the columns that rule is
-- written in, and indexes fast enough that the boot-time curated reads stay
-- cheap as user content grows.
--
-- ## Re-running
--
-- Every statement is idempotent — IF NOT EXISTS on the columns and indexes, and
-- DROP CONSTRAINT IF EXISTS before each FK is re-added — for 00018's reason: a
-- schema change re-run against data it already wrote must not fail on "already
-- exists". That idempotency is also why the package's migration-rewind tests
-- cannot simply unrecord version 22: a bare DELETE from goose_db_version would
-- leave this schema in place and the next Migrate would sail through as a
-- no-op, hiding a rewind that did not rewind. So
-- rewindOwnedDomainModelsMigration in ownedmodelsmigration_test.go rolls goose
-- back to version 20, which runs the Down block at the bottom of this file.
-- That block is therefore live test infrastructure rather than documentation:
-- it must keep restoring the ON DELETE SET NULL foreign keys and dropping the
-- indexes and columns added below, or the rewinding tests stop rewinding.
--
-- ## Fresh vs deployed databases
--
-- There is no backfill, and none is wanted. Every row that exists today —
-- seeded ca-hsr content and admin-ingested alignments alike — keeps a NULL
-- owner, which is precisely what "curated" means from here on. A fresh database
-- reaches the same state by seeding after this migration runs.

-- Ownership on the two models that lacked it. routes.description joins
-- scenarios.description as the field an owner actually wants to edit; it
-- defaults to '' so every existing route reads back unchanged.
ALTER TABLE routes   ADD COLUMN IF NOT EXISTS owner_id    uuid REFERENCES users (id) ON DELETE CASCADE;
ALTER TABLE routes   ADD COLUMN IF NOT EXISTS description text NOT NULL DEFAULT '';
ALTER TABLE stations ADD COLUMN IF NOT EXISTS owner_id    uuid REFERENCES users (id) ON DELETE CASCADE;

-- Plain btree rather than a partial index, because both access patterns matter.
-- Postgres btrees index NULLs, so one index serves the owner-scoped lists
-- (owner_id = $1) and the curated reads (owner_id IS NULL) alike. A
-- `WHERE owner_id IS NOT NULL` partial index would be smaller but could not
-- serve the curated reads at all — and those run on every boot, over tables
-- that now grow with user content.
CREATE INDEX IF NOT EXISTS routes_owner_id_idx    ON routes    (owner_id);
CREATE INDEX IF NOT EXISTS stations_owner_id_idx  ON stations  (owner_id);
CREATE INDEX IF NOT EXISTS scenarios_owner_id_idx ON scenarios (owner_id);
CREATE INDEX IF NOT EXISTS services_owner_id_idx  ON services  (owner_id);

-- Realign the two pre-existing owner FKs from SET NULL to CASCADE.
--
-- 00001 chose SET NULL when an unowned row meant nothing in particular. Under
-- the invariant above it means "curated and public", so SET NULL would promote
-- a deleted user's private scenario into the public compiled store — where
-- LoadStore compiles it on every boot, and a malformed one aborts that boot.
-- That is exactly the leak the ownership filter exists to prevent, arriving
-- through the back door.
--
-- There is no delete-user endpoint today, so this is latent rather than live,
-- which is what makes now the cheap moment to fix it. CASCADE also matches
-- user_services.owner_id (00005) and user_scenarios.owner_id (00006), so it is
-- the convention here rather than a departure from it.
ALTER TABLE scenarios DROP CONSTRAINT IF EXISTS scenarios_owner_id_fkey;
ALTER TABLE scenarios ADD  CONSTRAINT scenarios_owner_id_fkey
    FOREIGN KEY (owner_id) REFERENCES users (id) ON DELETE CASCADE;

ALTER TABLE services DROP CONSTRAINT IF EXISTS services_owner_id_fkey;
ALTER TABLE services ADD  CONSTRAINT services_owner_id_fkey
    FOREIGN KEY (owner_id) REFERENCES users (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE services DROP CONSTRAINT IF EXISTS services_owner_id_fkey;
ALTER TABLE services ADD  CONSTRAINT services_owner_id_fkey
    FOREIGN KEY (owner_id) REFERENCES users (id) ON DELETE SET NULL;

ALTER TABLE scenarios DROP CONSTRAINT IF EXISTS scenarios_owner_id_fkey;
ALTER TABLE scenarios ADD  CONSTRAINT scenarios_owner_id_fkey
    FOREIGN KEY (owner_id) REFERENCES users (id) ON DELETE SET NULL;

DROP INDEX IF EXISTS services_owner_id_idx;
DROP INDEX IF EXISTS scenarios_owner_id_idx;
DROP INDEX IF EXISTS stations_owner_id_idx;
DROP INDEX IF EXISTS routes_owner_id_idx;

ALTER TABLE stations DROP COLUMN IF EXISTS owner_id;
ALTER TABLE routes   DROP COLUMN IF EXISTS description;
ALTER TABLE routes   DROP COLUMN IF EXISTS owner_id;
