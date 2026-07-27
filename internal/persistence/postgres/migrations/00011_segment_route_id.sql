-- +goose Up
-- SPA-152: the corridor a segment is track of, so a client can group
-- "time between stations" once a scenario spans more than one corridor.
--
-- The route, not the service, is a segment's identity: CA HSR Express and
-- Local both run the Phase 1 spans, so no single service owns them.
--
-- Backfilled rather than added NULL-able: every existing segment belongs to
-- exactly one scenario, and every seeded scenario has exactly one route today,
-- so the mapping is unambiguous. Adding it NOT NULL keeps the read path free of
-- an "unknown route" case that the data can never produce.
--
-- Admin-ingested routes carry no scenario (00003), so they cannot be picked up
-- here. The ORDER BY only breaks a tie that a future multi-route seed could
-- introduce, and keeps the backfill deterministic if it ever does.
ALTER TABLE segments ADD COLUMN route_id uuid REFERENCES routes (id);

UPDATE segments s
SET route_id = (
    SELECT r.id FROM routes r
    WHERE r.scenario_id = s.scenario_id
    ORDER BY r.id
    LIMIT 1
)
WHERE s.route_id IS NULL;

-- A scenario with no routes cannot have had segments compiled against it, so
-- any row still NULL here is orphaned seed data; drop it rather than block.
DELETE FROM segments WHERE route_id IS NULL;

ALTER TABLE segments ALTER COLUMN route_id SET NOT NULL;
CREATE INDEX segments_route_id_idx ON segments (route_id);

-- +goose Down
DROP INDEX IF EXISTS segments_route_id_idx;
ALTER TABLE segments DROP COLUMN IF EXISTS route_id;
