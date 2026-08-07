-- +goose Up
-- Product analytics for the website (SPA-218), distinct from the structured
-- request logs already shipped to Grafana Cloud (SPA-194/199): those cover
-- infra observability (errors, latency), this covers product usage (page
-- views, feature interactions) for an admin to read inside the product
-- itself, per the SPA-208 spike.
--
-- ## The privacy decision
--
-- This table stores no identifier of any kind — no cookie id, no session id,
-- no IP address, no user agent, nothing that could tie two rows to the same
-- visitor. That is deliberate, not an oversight: SPA-218 scopes out unique
-- visitor tracking that depends on a persistent cross-session identifier, and
-- the only way to make that a hard guarantee rather than a policy someone
-- could quietly erode is to give the schema nowhere to put one. A table that
-- cannot name who generated a row cannot need a consent banner to keep
-- collecting them.
--
-- One consequence: the ingestion endpoint's per-IP rate limiting and bot
-- filtering both happen in front of this table, not through it — the client
-- IP they inspect is used in memory for that request and is never written
-- here.
--
-- created_at is the server's own insert time rather than a client-supplied
-- timestamp. The ingestion endpoint is public and unauthenticated by design,
-- so a client-reported "when this happened" cannot be trusted for the
-- aggregate reads admins run against this table; the batching delay between
-- an event firing in the browser and its batch flushing is seconds, not
-- something worth trusting a client clock over.
CREATE TABLE analytics_events (
    id uuid PRIMARY KEY,
    -- The taxonomy already defined in the website's src/analytics/types.ts
    -- (page_view, origin_search, mode_toggle, isochrone_request,
    -- isochrone_error). Not a CHECK constraint: the handler is what enforces
    -- the known set, so a new event type ships as a handler change alone
    -- rather than a handler change plus a migration.
    event_type text NOT NULL,
    -- Populated for page_view, empty for every other event type today. Its
    -- own column rather than folded into properties because "which page" is
    -- the one dimension every admin read groups or filters by.
    path text NOT NULL DEFAULT '',
    -- The event's type-specific fields verbatim (mode, travel_mode,
    -- duration_minutes, status, query, result_count, ...), stored as the
    -- client sent them. A jsonb catch-all rather than a column per field
    -- means a new event type's shape needs no migration to start being
    -- recorded.
    properties jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Every admin read groups by type and/or buckets by time; this is the one
-- index that serves both without a second.
CREATE INDEX analytics_events_type_created_idx ON analytics_events (event_type, created_at);

-- +goose Down
DROP TABLE IF EXISTS analytics_events;
