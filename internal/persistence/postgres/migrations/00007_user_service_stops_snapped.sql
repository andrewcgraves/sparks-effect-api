-- +goose Up
-- Stops are stored snapped (SPA-108).
--
-- Before this, one stop had three coordinates: what the user typed and we
-- stored raw, what the snap preview showed them, and what the compiler derived
-- when it re-projected at compile time. Nothing reconciled them. From here the
-- write path projects every stop onto its route and stores the result, so there
-- is one coordinate, and re-deriving it downstream is a no-op rather than a
-- second opinion.
--
-- There is no column to add: stops live in the `stops` jsonb document, so the
-- two new per-stop fields (`chainage_m`, `offset_m`) arrive with the Go struct.
-- What this migration contributes is the invariant that they are always there,
-- and the assertion below that no pre-snap row is left behind carrying raw
-- coordinates the invariant would silently bless.
--
-- Scope: `user_services` only. The `service_stops` table from 00001 belongs to
-- the seeded `Service` aggregate, whose stops reference shared `stations` rows
-- and are compiled through the hand-authored run-time table rather than by
-- projecting onto route geometry. Nothing snaps them, so there is nothing here
-- for them to hold.
--
-- ## Why offset_m is persisted and not merely returned
--
-- SPA-108's own acceptance criteria only need chainage. offset_m is here for
-- SPA-113: its recommended fix for the off-route tolerance undermining
-- SPA-109's co-located-stop merge is to widen the merge radius by the snapping
-- uncertainty each stop carries, which is exactly this number, and it is needed
-- at compile time rather than at write time. Adding the column afterwards would
-- mean recomputing offsets against route geometry for rows whose raw positions
-- had already been overwritten with their snapped ones — recoverable, but
-- needless. It is cheap to carry and expensive to retrofit, so it is carried.
--
-- Note the caveat documented on transit.ServiceStopPoint.OffsetM: offset
-- describes one write, not the stop. A client that resubmits the coordinate a
-- previous write returned has moved the stop zero metres, so a stored offset
-- decays to 0 on re-save. SPA-113 has to reckon with that before leaning on it.
--
-- ## Backfill strategy: assert, do not migrate
--
-- Row count observed when this was written: **0 user_services rows** in the
-- development and CI databases. Production was not reachable from where this
-- was authored, so the assertion below is what actually establishes the fact at
-- deploy time rather than assuming it.
--
-- The reason to expect zero everywhere: accounts are invite-only and
-- admin-provisioned, the service authoring UI (SPA-86) has never shipped, and
-- the frontend's create call cannot succeed regardless — `ServiceInput` in
-- sparks-effect-website `src/api/authoring/types.ts` is
-- `{name, stops, vehicle, frequency_windows}` with no route field of any kind,
-- confirmed at `main`. A service cannot be created without naming a route.
--
-- So the quarantine mechanism SPA-108 sketched (flag over-threshold rows, make
-- the next PUT reject until fixed) is machinery for data that does not exist.
-- Rather than ship and maintain it unexercised, this migration refuses to run
-- if the premise is wrong. That is a deployment blocker by design: the
-- alternative is snapping rows in SQL, which would mean reimplementing
-- equirectangular projection in PL/pgSQL and having it drift from
-- internal/physics — a worse outcome than a failed deploy and a human decision.
--
-- If this assertion ever fires, do not weaken it. Snap the offending rows with
-- a one-off Go program that calls physics.SnapStops (the same code the write
-- path uses), decide per row what to do with anything over
-- transit.OffRouteThresholdM, and then re-run.
--
-- A future reader asking "were there no quarantined rows because the rule held,
-- or because the table was empty?" — it was empty.

-- +goose StatementBegin
DO $$
DECLARE
    pre_snap bigint;
BEGIN
    SELECT count(*) INTO pre_snap FROM user_services;
    IF pre_snap > 0 THEN
        RAISE EXCEPTION
            'user_services holds % row(s) written before stops were snapped (SPA-108). '
            'Their coordinates are raw and their chainage is unknown, and snapping them '
            'needs the projection in internal/physics, which SQL cannot reach. Snap them '
            'with a one-off Go program and re-run this migration. See the comment at the '
            'head of 00007_user_service_stops_snapped.sql.', pre_snap;
    END IF;
END $$;
-- +goose StatementEnd

-- Every stored stop carries the products of its snap. This is what makes
-- chainage safe to read as fact downstream: without it a missing key would
-- decode to a chainage of 0, which is a real position on every route, so a
-- pre-snap row would look like a stop at the start of the line rather than like
-- missing data.
ALTER TABLE user_services
    ADD CONSTRAINT user_services_stops_are_snapped
    CHECK (NOT jsonb_path_exists(
        stops,
        '$[*] ? (!exists(@.chainage_m) || !exists(@.offset_m))'));

-- +goose Down
ALTER TABLE user_services DROP CONSTRAINT IF EXISTS user_services_stops_are_snapped;
