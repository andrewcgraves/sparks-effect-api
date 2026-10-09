-- +goose Up
-- The one-line subtext a curated scenario shows under its name (SPA-415).
--
-- 00025 gave authored services a subtext; this gives scenarios the same word,
-- column shape and 140-character bound, so the ca-hsr page can read
-- "Electrified · High-speed rail · Greenfield" from the API instead of
-- hard-coding it (SPA-368 removed the hard-coded line).
--
-- ## Why NOT NULL DEFAULT '' and no backfill
--
-- 00025's reasons: every existing row reads back unchanged as "no subtext",
-- with no nullable pointer threaded through the Go model. Curated rows get
-- their value from scenario.yaml through ReconcileSeed on the next boot
-- (ADR-0002), so this migration writes no data. Authored scenarios keep ''.
--
-- There is no length CHECK. The bound is enforced where the value enters:
-- seed loading rejects an over-long YAML subtext.
--
-- ## Re-running
--
-- ADD COLUMN IF NOT EXISTS for 00018's reason: a re-run against a database
-- that already has the column must not fail on "already exists".

ALTER TABLE scenarios ADD COLUMN IF NOT EXISTS subtext text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE scenarios DROP COLUMN IF EXISTS subtext;
