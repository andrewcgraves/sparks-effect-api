-- +goose Up
-- The one-line subtext an authored service shows under its name (SPA-351).
--
-- A service's public page wants three pieces of prose: name, subtext and
-- description. user_services has carried name and description since 00005;
-- this adds the third. The word is ADR-0005's: `subtext` in the column, the JSON
-- and the website's type, for the descriptor the ca-hsr page hard-codes as
-- "Electrified · High-speed rail · Greenfield". Not subtitle, not tagline.
--
-- ## Why NOT NULL DEFAULT ''
--
-- 00005 made the same choice for description, and 00022 for routes.description,
-- for the same reason: every row that exists today reads back unchanged, as
-- "no subtext", with no backfill and no nullable pointer threaded through the
-- Go model to tell "unset" from "empty". The two mean the same thing here.
--
-- There is no length CHECK. The bound lives in UserService.Validate() as a
-- typed fault, where a client can read which field broke which rule; a CHECK
-- would only turn the same mistake into a 500.
--
-- ## Re-running
--
-- ADD COLUMN IF NOT EXISTS for 00018's reason: a schema change re-run against
-- data it already wrote must not fail on "already exists". It is also what lets
-- the package's migration-rewind tests unrecord this version with a bare
-- DELETE and bring the database forward again.

ALTER TABLE user_services ADD COLUMN IF NOT EXISTS subtext text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE user_services DROP COLUMN IF EXISTS subtext;
