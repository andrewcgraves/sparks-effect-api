-- +goose Up
-- An admin can move a service to a named account without an offer (SPA-390),
-- for an owner who has left, whose account is disabled, or who was the wrong
-- person. The move is recorded as a service_handovers row like any accepted
-- handover, from the old owner to the new, so one table holds every change
-- of owner; forced_by names the admin who made it and is NULL on a handover
-- the recipient accepted themselves.
--
-- A forced row is written already closed: status 'accepted', decided_at and
-- expires_at both now(). Nothing was ever pending, so there was nothing to
-- expire, and 00032's one-pending index never sees it. The
-- service_handovers_forced_is_accepted check holds that shape: an admin's id
-- can only sit on an accepted row, never on an offer that is pending or was
-- declined, cancelled or expired.
--
-- ## Foreign key
--
-- ON DELETE SET NULL, unlike from_user_id and to_user_id: the record of a
-- move belongs to the service's history, not to the admin who made it, so it
-- would outlive their row. Users are disabled, never deleted (00030), so this
-- is documentation more than behaviour.
--
-- ## Re-running
--
-- ADD COLUMN IF NOT EXISTS, and the constraint dropped before it is added,
-- for 00018's reason: a schema change re-run against data it already wrote
-- must not fail on "already exists". It is also what lets the package's
-- migration-rewind tests unrecord this version with a bare DELETE and bring
-- the database forward again.

ALTER TABLE service_handovers
    ADD COLUMN IF NOT EXISTS forced_by uuid REFERENCES users (id) ON DELETE SET NULL;

ALTER TABLE service_handovers
    DROP CONSTRAINT IF EXISTS service_handovers_forced_is_accepted;
ALTER TABLE service_handovers
    ADD CONSTRAINT service_handovers_forced_is_accepted
        CHECK (forced_by IS NULL OR status = 'accepted');

-- +goose Down
ALTER TABLE service_handovers DROP CONSTRAINT IF EXISTS service_handovers_forced_is_accepted;
ALTER TABLE service_handovers DROP COLUMN IF EXISTS forced_by;
