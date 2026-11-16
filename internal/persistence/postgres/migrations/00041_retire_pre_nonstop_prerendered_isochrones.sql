-- +goose Up
-- Retire the five ca-hsr prerendered isochrones captured before the HSR
-- Nonstop ran (SPA-556).
--
-- ## Why
--
-- SPA-534 added the HSR Nonstop, a fifth ca-hsr service. Any change to the
-- scenario's services leaves every saved payload describing the old network,
-- and the API flags all five outdated. They were captured again on staging
-- against the graph with the nonstop, and the new captures ship under new ids
-- (…0010 to …0014). San Francisco's now starts from the Salesforce Transit
-- Center ("Downtown SF") instead of the Ferry Building, and reaches Los
-- Angeles within its 180 minutes. As with 00035, 00038 and 00039, the old
-- seed files are gone, but the seeder only inserts. Without this migration
-- all five rows would stay in every deployed list, flagged outdated.
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
    '00000000-0000-4006-8001-00000000000b'::uuid,
    '00000000-0000-4006-8001-00000000000c'::uuid,
    '00000000-0000-4006-8001-00000000000d'::uuid,
    '00000000-0000-4006-8001-00000000000e'::uuid,
    '00000000-0000-4006-8001-00000000000f'::uuid
);

-- +goose Down
-- Deliberately empty.
--
-- Their seed files are gone, so nothing could restore their payloads, and
-- rolling the application back does not need them: the five entries that
-- replace them stay harmless.
