-- +goose Up
-- Retire ca-hsr's two 90-minute transit prerendered isochrones (SPA-532).
--
-- ## Why
--
-- "San Francisco (Transbay) - 90 min by transit" only reached Gilroy, which a
-- train already serves today, and "Los Angeles (Union Station) - 90 min by
-- transit" stopped well short of the Central Valley. Neither showed what the
-- HSR line adds. SPA-532 replaces both with 135-minute captures that reach
-- Fresno and Bakersfield, under new ids (…0009 and …000a).
--
-- These two were not outdated: they were captured against the current graph.
-- They are retired because the home page leads with this list and should not
-- show both a 90- and a 135-minute plot from the same origin. As with 00035,
-- their seed files are gone, but the seeder only inserts. Without this
-- migration both rows would stay in every deployed list.
--
-- ## Why this is inert on a fresh database
--
-- Migrations run before the prerendered seed, and the seed no longer ships
-- either id. On an empty database the DELETE touches zero rows, and nothing
-- puts them back afterwards.
--
-- Re-running is safe: deleting rows that are already gone is a no-op.

DELETE FROM prerendered_isochrones
WHERE id IN (
    '00000000-0000-4006-8001-000000000003'::uuid,
    '00000000-0000-4006-8001-000000000006'::uuid
);

-- +goose Down
-- Deliberately empty.
--
-- Their seed files are gone, so nothing could restore their payloads, and
-- rolling the application back does not need them: the 135-minute entries
-- that replace them stay harmless.
