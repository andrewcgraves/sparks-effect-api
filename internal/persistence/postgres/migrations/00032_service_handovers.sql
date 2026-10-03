-- +goose Up
-- A handover moves an authored service from one owner to another (SPA-388).
-- The owner offers it to another account, and the recipient accepts or
-- declines; the owner may cancel while it is pending. This migration and
-- SPA-388 cover the offer and the two refusals. Accepting is SPA-389.
--
-- "Handover", not "transfer": CONTEXT.md already spends "transfer" on a rider
-- changing service at an interchange (no transfer edge, boarding wait is not a
-- transfer penalty, SPA-225, SPA-347).
--
-- ## One pending offer per service
--
-- service_handovers_one_pending is a unique partial index on user_service_id
-- over status = 'pending'. Closed rows (accepted, declined, cancelled,
-- expired) stay as history and do not count.
--
-- ## Expiry is lazy
--
-- Nothing rewrites a row when expires_at passes. Every read treats a pending
-- row past expires_at as expired. That leaves a stale 'pending' row holding
-- the unique index, so an offer first marks the service's expired rows
-- 'expired' (decided_at = expires_at) in the same transaction as its insert.
--
-- ## Foreign keys
--
-- user_service_id cascades from user_services, so deleting a service deletes
-- its offers. from_user_id and to_user_id join the ON DELETE CASCADE set that
-- 00030 lists; users are disabled, never deleted, so these rarely fire.
--
-- ## Gap at 00031
--
-- 00031 belongs to SPA-387 (account_tokens), which may land after this one.
--
-- ## Re-running
--
-- IF NOT EXISTS for 00018's reason: a schema change re-run against data it
-- already wrote must not fail on "already exists". It is also what lets the
-- package's migration-rewind tests unrecord this version with a bare DELETE
-- and bring the database forward again.

CREATE TABLE IF NOT EXISTS service_handovers (
    id              uuid PRIMARY KEY,
    user_service_id uuid NOT NULL REFERENCES user_services (id) ON DELETE CASCADE,
    from_user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    to_user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    status          text NOT NULL DEFAULT 'pending',
    created_at      timestamptz NOT NULL DEFAULT now(),
    decided_at      timestamptz,
    expires_at      timestamptz NOT NULL,
    CONSTRAINT service_handovers_status_check
        CHECK (status IN ('pending', 'accepted', 'declined', 'cancelled', 'expired')),
    CONSTRAINT service_handovers_decided_iff_closed
        CHECK ((status = 'pending') = (decided_at IS NULL)),
    CONSTRAINT service_handovers_not_to_self
        CHECK (from_user_id <> to_user_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS service_handovers_one_pending
    ON service_handovers (user_service_id) WHERE status = 'pending';

-- GET /api/me/handovers lists only pending offers, by either party.
CREATE INDEX IF NOT EXISTS service_handovers_from_pending
    ON service_handovers (from_user_id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS service_handovers_to_pending
    ON service_handovers (to_user_id) WHERE status = 'pending';

-- +goose Down
DROP TABLE IF EXISTS service_handovers;
