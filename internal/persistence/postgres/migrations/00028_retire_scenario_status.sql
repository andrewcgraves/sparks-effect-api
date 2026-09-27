-- +goose Up
-- scenarios.status is retired (SPA-362).
--
-- 00001 gave scenarios a free-text status, and the seed YAML filled ca-hsr's
-- with "published". Nothing ever filtered on it — ListCuratedScenarios decides
-- what is curated from owner_id IS NULL alone — and the website never read it.
-- ADR-0005 then defined *published* as an authored service with a publication,
-- which left a column holding "published" that meant nothing. Section 6 of that
-- ADR retires it rather than reusing it as publication state: a curated scenario
-- is public because it is curated, not because it was published.
--
-- The Go model, the seed YAML and GET /api/scenarios drop the field in the same
-- change, so no reader is left behind to find the column missing.
--
-- ## Seed reconciliation
--
-- ADR-0002's reconciliation compares a stored scenario with the embedded seed
-- as JSON. Both sides lose the field together, so a database that was already
-- reconciled stays reconciled: the next boot compares id, slug, name and
-- description, finds them equal, and writes nothing.
--
-- ## Re-running
--
-- DROP COLUMN IF EXISTS for 00018's reason: a schema change re-run against a
-- database it already changed must not fail. It is also what lets the
-- package's migration-rewind tests unrecord this version with a bare DELETE and
-- bring the database forward again.
--
-- ## Down
--
-- Restores the column in 00001's shape. The values are not restored: they were
-- dead data, and nothing reads them.

ALTER TABLE scenarios DROP COLUMN IF EXISTS status;

-- +goose Down
ALTER TABLE scenarios ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT '';
