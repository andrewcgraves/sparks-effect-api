-- +goose Up
-- The wording a network's plot button reads once a start is chosen (SPA-542).
--
-- A template, not finished text: the website fills {minutes} and {mode} from
-- the form as the visitor changes them, so ca-hsr's
-- "Show a {minutes}-minute HSR ride and {mode}" reads
-- "Show a 60-minute HSR ride and walk".
--
-- ## Why NOT NULL DEFAULT '' and no backfill
--
-- 00036's reasons: every existing row reads back as "no plot label", which the
-- website takes as "use the default wording", with no nullable pointer
-- threaded through the Go model. Curated rows get their value from
-- scenario.yaml through ReconcileSeed on the next boot (ADR-0002), so this
-- migration writes no data. Authored scenarios keep ''.
--
-- An API that predates this column never reads it, and a website that
-- predates the field ignores it, so the two can deploy in either order.
--
-- There is no length CHECK. The bound and the placeholder check are enforced
-- where the value enters: seed loading rejects a bad YAML plot_label.
--
-- ## Re-running
--
-- ADD COLUMN IF NOT EXISTS for 00018's reason: a re-run against a database
-- that already has the column must not fail on "already exists".

ALTER TABLE scenarios ADD COLUMN IF NOT EXISTS plot_label text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE scenarios DROP COLUMN IF EXISTS plot_label;
