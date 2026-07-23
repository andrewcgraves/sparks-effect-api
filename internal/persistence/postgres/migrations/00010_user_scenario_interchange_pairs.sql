-- +goose Up
-- SPA-120: a scenario's explicit declared interchange — the owner's
-- assertion that two stops, each on a member service, are the same place.
-- Folded into MergeColocatedStops' clustering regardless of geometric
-- distance, alongside the existing proximity merge.
--
-- jsonb, not a relational table: a pair has no identity beyond the two stop
-- identities it names, and the whole list is always read and rewritten
-- together with the rest of the scenario — the same reasoning user_services
-- gives for its embedded stops.
ALTER TABLE user_scenarios ADD COLUMN interchange_pairs jsonb NOT NULL DEFAULT '[]'::jsonb;

-- +goose Down
ALTER TABLE user_scenarios DROP COLUMN IF EXISTS interchange_pairs;
