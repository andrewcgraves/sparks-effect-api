-- +goose Up
-- Give a job a target beyond the seeded scenario (SPA-106).
--
-- Until now a compile job pointed at exactly one thing: a seeded `scenarios`
-- row, via `jobs.scenario_id`. The user-authored stack — user_services and the
-- user_scenarios that curate them — had no way to trigger a compile, because
-- there was no column that could name one as the job's target and
-- `scenario_id`'s FK is a hard reference to the seeded table. Writing a
-- `user_scenarios.id` there fails the constraint.
--
-- ## Kind is the discriminator; the FKs stay real
--
-- Rather than a bare polymorphic `target_id` with no referential integrity, or
-- a parallel `target_type` column that must forever agree with `kind`, the job
-- gains two more nullable FKs and leans on `kind` — already the "what sort of
-- job is this" field — to say which one is populated. `kind` moves from the
-- single value 'compile' to one of compile_scenario / compile_user_scenario /
-- compile_user_service (see transit's Job kind constants).
--
-- The delete semantics differ deliberately. The seeded `scenario_id` keeps its
-- ON DELETE SET NULL: a seeded scenario outlives edits and a stale job may
-- legitimately survive it. The user targets CASCADE, because a compiled graph
-- for a user scenario or service the owner has since deleted is unreachable
-- garbage that should go with it rather than linger behind a stale job id.
--
-- The CHECK is `<= 1`, not `= 1`: the seeded FK's SET NULL can legitimately
-- leave a job with no target at all, and a job mid-insert should not be forced
-- to have one before its target column is set.
--
-- ## compiled_service_ids: what the job actually compiled
--
-- A user scenario is a curated set of member services, and a member can be
-- deleted after a compile — user_scenario_services CASCADEs the membership row
-- away, so the scenario's *current* membership no longer mentions it. Recording
-- the member service ids this job compiled makes that detectable later (SPA-116
-- compares the two to spot a deleted member and mark the graph stale). It is a
-- plain uuid[] rather than an FK array on purpose: it is a snapshot of what was
-- compiled, so a since-deleted member must remain listed, which a CASCADE FK
-- would defeat. Populated for every kind (for the seeded and single-service
-- cases it is simply the services the graph contains); queried far more cheaply
-- as a column than by digging service ids out of the result jsonb.
ALTER TABLE jobs ADD COLUMN user_scenario_id uuid REFERENCES user_scenarios (id) ON DELETE CASCADE;
ALTER TABLE jobs ADD COLUMN user_service_id  uuid REFERENCES user_services  (id) ON DELETE CASCADE;
ALTER TABLE jobs ADD COLUMN compiled_service_ids uuid[];
ALTER TABLE jobs ADD CONSTRAINT jobs_one_target CHECK (
  (scenario_id IS NOT NULL)::int + (user_scenario_id IS NOT NULL)::int
  + (user_service_id IS NOT NULL)::int <= 1
);

-- Kind moves from the single value 'compile' to 'compile_scenario' (see the
-- Job kind constants). Backfill any existing seeded compile rows so a graph
-- already compiled stays reachable by GetLatestSucceededJob, which now filters
-- on the new value. (No FK indexes are added here, matching the jobs table's
-- existing convention of indexing none of its FKs.)
UPDATE jobs SET kind = 'compile_scenario' WHERE kind = 'compile';

-- +goose Down
UPDATE jobs SET kind = 'compile' WHERE kind = 'compile_scenario';
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_one_target;
ALTER TABLE jobs DROP COLUMN IF EXISTS compiled_service_ids;
ALTER TABLE jobs DROP COLUMN IF EXISTS user_service_id;
ALTER TABLE jobs DROP COLUMN IF EXISTS user_scenario_id;
