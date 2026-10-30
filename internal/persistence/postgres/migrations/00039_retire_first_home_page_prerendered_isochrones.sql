-- +goose Up
-- Retire the six ca-hsr prerendered isochrones the home page led with before
-- its picks were redrawn.
--
-- ## Why
--
-- The home page's set is now five new captures under new ids (…000b to
-- …000f): San Francisco (Ferry Building) at 180 min by transit, Fresno
-- (Chukchansi Park) at 120 min on foot, San Jose (Diridon) at 90 min by bike,
-- Bakersfield at 45 min on foot and Los Angeles (Union Station) at 120 min by
-- transit. None of the six below was outdated; they are retired because the
-- home page leads with this list and should show the new set alone. As with
-- 00035 and 00038, their seed files are gone, but the seeder only inserts.
-- Without this migration all six rows would stay in every deployed list.
--
-- ## Why this is inert on a fresh database
--
-- Migrations run before the prerendered seed, and the seed no longer ships
-- any of these ids. On an empty database the DELETE touches zero rows, and
-- nothing puts them back afterwards.
--
-- Re-running is safe: deleting rows that are already gone is a no-op.

DELETE FROM prerendered_isochrones
WHERE id IN (
    '00000000-0000-4006-8001-000000000004'::uuid,
    '00000000-0000-4006-8001-000000000005'::uuid,
    '00000000-0000-4006-8001-000000000007'::uuid,
    '00000000-0000-4006-8001-000000000008'::uuid,
    '00000000-0000-4006-8001-000000000009'::uuid,
    '00000000-0000-4006-8001-00000000000a'::uuid
);

-- +goose Down
-- Deliberately empty.
--
-- Their seed files are gone, so nothing could restore their payloads, and
-- rolling the application back does not need them: the five entries that
-- replace them stay harmless.
