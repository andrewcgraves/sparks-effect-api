-- +goose Up
-- The corridor a segment is track of (SPA-152), so a client can group
-- "time between stations" once a scenario spans more than one corridor.
--
-- The route, not the service, is a segment's identity: CA HSR Express and
-- Local both run the Phase 1 spans, so no single service owns them, while
-- Brightline West is a distinct route. Keying by service would mean picking
-- one of two equally-entitled services arbitrarily.
--
-- ## Backfill strategy: assert the premise, then backfill
--
-- The deployed database is already seeded and SeedIfEmpty will not re-seed a
-- populated one, so this migration — not the seed — is what gives existing
-- rows their route. Every seeded scenario has exactly one route today
-- (ca-hsr's Phase 1 alignment; Brightline West is still commented out in
-- routes.yaml), which makes the mapping unambiguous.
--
-- That premise is asserted rather than assumed, because the two ways it can
-- break both fail silently if left to a plain UPDATE:
--
--   * A scenario with several routes would need per-segment knowledge of which
--     alignment each span belongs to, which SQL cannot recover — the authored
--     YAML is where that lives. Picking one route (say, the lowest id) would
--     mis-key every segment of the other corridors with no error, and this is
--     precisely the multi-corridor case the ticket exists to unlock, so it is
--     a premise that is expected to break eventually.
--   * A scenario with no route leaves rows that cannot satisfy NOT NULL.
--     Deleting them would silently destroy seeded run times that SeedIfEmpty
--     will never restore.
--
-- If this assertion fires, do not weaken it. Add route_id to that scenario's
-- segment_run_times.yaml, backfill the affected rows from it with a one-off
-- program, and re-run. A failed deploy and a human decision beats mis-keyed
-- or deleted travel times.
--
-- Admin-ingested routes carry no scenario (00003), so they are invisible here
-- and cannot affect the count.

-- +goose StatementBegin
DO $$
DECLARE
    bad_scenario uuid;
    route_count  bigint;
BEGIN
    SELECT s.scenario_id, count(DISTINCT r.id)
    INTO bad_scenario, route_count
    FROM segments s
    LEFT JOIN routes r ON r.scenario_id = s.scenario_id
    GROUP BY s.scenario_id
    HAVING count(DISTINCT r.id) <> 1
    LIMIT 1;

    IF bad_scenario IS NOT NULL THEN
        RAISE EXCEPTION
            'scenario % has % route(s) but holds travel-time segments (SPA-152). '
            'Deriving each segment''s route needs the authored segment_run_times.yaml, '
            'which SQL cannot reach. Backfill those rows with a one-off program and '
            're-run this migration. See the comment at the head of '
            '00011_segment_route_id.sql.', bad_scenario, route_count;
    END IF;
END $$;
-- +goose StatementEnd

-- ON DELETE CASCADE matches segments.scenario_id (00001) and
-- user_services.route_id (00005): a segment is track of its route, so it has
-- no meaning once that route is gone.
ALTER TABLE segments ADD COLUMN route_id uuid REFERENCES routes (id) ON DELETE CASCADE;

-- Unambiguous: the assertion above established that every scenario holding
-- segments has exactly one route.
UPDATE segments s
SET route_id = r.id
FROM routes r
WHERE r.scenario_id = s.scenario_id;

ALTER TABLE segments ALTER COLUMN route_id SET NOT NULL;

-- Backs the cascade above rather than any read path: GetTravelTimes still
-- selects by scenario_id. Without it, deleting a route seq-scans segments to
-- find the rows to cascade.
CREATE INDEX segments_route_id_idx ON segments (route_id);

-- +goose Down
DROP INDEX IF EXISTS segments_route_id_idx;
ALTER TABLE segments DROP COLUMN IF EXISTS route_id;
