-- +goose Up
-- An optional call to action a curated scenario shows under its description:
-- a button label and the https link it opens (SPA-549). ca-hsr's is "Sign
-- here", linking to the petition to build high-speed rail sooner.
--
-- ## Why two columns and NOT NULL DEFAULT ''
--
-- 00036's reasons for subtext: every existing row reads back unchanged, as "no
-- call to action", with no nullable pointer threaded through the Go model.
-- Curated rows get their values from scenario.yaml through ReconcileSeed on
-- the next boot (ADR-0002), so this migration writes no data. Authored
-- scenarios have no way to set one and keep ''.
--
-- There is no CHECK. Seed loading is where the values enter, and it rejects a
-- label without a link (or the reverse), an over-long label, and any link that
-- is not an absolute https URL.
--
-- ## Re-running
--
-- ADD COLUMN IF NOT EXISTS for 00018's reason: a re-run against a database
-- that already has the columns must not fail on "already exists".

ALTER TABLE scenarios ADD COLUMN IF NOT EXISTS call_to_action_label text NOT NULL DEFAULT '';
ALTER TABLE scenarios ADD COLUMN IF NOT EXISTS call_to_action_url text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE scenarios DROP COLUMN IF EXISTS call_to_action_url;
ALTER TABLE scenarios DROP COLUMN IF EXISTS call_to_action_label;
