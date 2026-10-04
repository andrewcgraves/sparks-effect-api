-- +goose Up
-- Retire ca-hsr's two original prerendered isochrones (SPA-439).
--
-- ## What was wrong
--
-- "San Jose - 240 min by bike" and "Burbank Airport - 240 min on foot" were
-- captured against a ca-hsr graph without the HSR Express. SPA-464 un-parked
-- it, so on any database where ReconcileSeed has run, both entries report
-- outdated: their service snapshot no longer matches the scenario's
-- membership (transit.MembershipStale). They are not merely mislabelled. A
-- walk or bike isochrone is still a chain through the HSR graph (the mode
-- only picks the access and egress costing), so their payloads describe a
-- network that no longer exists.
--
-- SPA-439 replaces them with a set captured against the current graph, under
-- new ids. Their seed files are gone from internal/transit/data/scenarios/
-- ca-hsr/prerendered, but the seeder only inserts. Without this migration
-- the two rows would sit in every deployed list as outdated forever. The
-- home page leads with this list, and its launch criterion is that none of
-- it is outdated.
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
    '00000000-0000-4006-8001-000000000001'::uuid,
    '00000000-0000-4006-8001-000000000002'::uuid
);

-- +goose Down
-- Deliberately empty.
--
-- The rows hold payloads of a graph ca-hsr no longer compiles to. Putting
-- them back would only restore two outdated entries, and rolling the
-- application back does not need them.
