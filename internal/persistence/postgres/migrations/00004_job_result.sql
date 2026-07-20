-- +goose Up
-- The async compile job's output (SPA-82): a physics-compiled TransitGraph,
-- stored as jsonb alongside the job that produced it. NULL until the job
-- succeeds; queued/running/failed jobs never populate it.
ALTER TABLE jobs ADD COLUMN result jsonb;

-- +goose Down
ALTER TABLE jobs DROP COLUMN result;
