-- +goose Up
-- Per-service and per-scenario boarding-wait overrides (SPA-237).
--
-- NULL means inherit from the next level of
-- service override > scenario override > global default > none, so existing
-- rows keep the SPA-236 behaviour after this migration: nothing stored, every
-- compile still charges the global (default none).
--
-- boarding_wait_policy is one of none / half_headway / full_headway / fixed.
-- boarding_wait_fixed_secs is the companion for fixed; ignored otherwise and
-- left NULL for the other kinds.
--
-- IF NOT EXISTS rather than a bare ADD COLUMN: a schema change re-run against
-- data it already wrote (rather than a full rewind) must not fail on "column
-- already exists".

ALTER TABLE user_services
    ADD COLUMN IF NOT EXISTS boarding_wait_policy text,
    ADD COLUMN IF NOT EXISTS boarding_wait_fixed_secs integer;

ALTER TABLE user_scenarios
    ADD COLUMN IF NOT EXISTS boarding_wait_policy text,
    ADD COLUMN IF NOT EXISTS boarding_wait_fixed_secs integer;

ALTER TABLE services
    ADD COLUMN IF NOT EXISTS boarding_wait_policy text,
    ADD COLUMN IF NOT EXISTS boarding_wait_fixed_secs integer;

-- +goose Down
ALTER TABLE user_services
    DROP COLUMN IF EXISTS boarding_wait_policy,
    DROP COLUMN IF EXISTS boarding_wait_fixed_secs;

ALTER TABLE user_scenarios
    DROP COLUMN IF EXISTS boarding_wait_policy,
    DROP COLUMN IF EXISTS boarding_wait_fixed_secs;

ALTER TABLE services
    DROP COLUMN IF EXISTS boarding_wait_policy,
    DROP COLUMN IF EXISTS boarding_wait_fixed_secs;
