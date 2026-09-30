-- +goose Up
-- Transit egress polygons are a function of the time of day the rider leaves
-- the station, and below the contour ceiling that time is recoverable from the
-- key only with the chain's budget beside the contour (SPA-326).
--
-- ## Why this column exists
--
-- Since SPA-274 the worker departs each egress isochrone at the chain's
-- departure plus the whole journey to that station, and asks for a contour of
-- the budget minus that same journey. The two are exactly complementary: two
-- chains land on one contour_mins precisely when their journeys differ by the
-- difference in their budgets, and their polygons were then computed that far
-- apart in the timetable. Budget 90 with a 10-minute access leg and budget 120
-- with a 40-minute one both reach a station with 79 minutes left, 30 minutes
-- apart, and shared one row. With the UI's presets the worst such gap was 195
-- minutes; through the API it was unbounded.
--
-- budget_mins is NULL on exactly departs_on's condition: walk/bike/drive send
-- Valhalla no departure, so the budget cannot change their polygon and keying
-- on it would only fragment their rows.
--
-- ## Why the uniqueness changes as it does
--
-- 00024's reasoning, one column wider: UNIQUE NULLS NOT DISTINCT keeps one
-- walk/bike/drive row per (graph, station, mode, contour), because two NULL
-- budgets collide, and lets two real budgets be two rows.
--
-- ## Existing transit rows
--
-- Every transit row already stored has a NULL budget, and no transit lookup
-- from a worker that sends budget_mins will ever match NULL again. Up deletes
-- them rather than leaving them for their compile job's foreign key to
-- collect: the seeded corridor's compile job lives indefinitely, so "orphaned"
-- would mean "forever". The transit cache goes cold either way. Walk/bike/drive
-- rows are untouched and stay warm. An un-updated worker still running when
-- this lands writes NULL-budget transit rows again until it is replaced; those
-- are few, and are the same dead weight this DELETE clears.
--
-- ## Deploy order
--
-- API first, then the worker. The constraint keeps its name, so the Put's
-- ON CONFLICT ON CONSTRAINT isochrone_cache_key needs no coordination, and an
-- old worker sends no budget_mins, which the API stores and matches as NULL —
-- the pre-SPA-326 behaviour, unchanged. A new worker against an old API is the
-- wrong order: the old API drops the field, answers with budget-less keys the
-- worker cannot match, and every transit lookup misses. That degrades to
-- computing the call (SPA-186); it never serves a wrong polygon.
--
-- IF NOT EXISTS / IF EXISTS throughout, for 00018's reason: a re-run against
-- a schema this already changed must not fail.

ALTER TABLE isochrone_cache
    ADD COLUMN IF NOT EXISTS budget_mins integer;

ALTER TABLE isochrone_cache
    DROP CONSTRAINT IF EXISTS isochrone_cache_key;

ALTER TABLE isochrone_cache
    ADD CONSTRAINT isochrone_cache_key
    UNIQUE NULLS NOT DISTINCT (compile_job_id, station_slug, mode, contour_mins, departs_on, budget_mins);

DELETE FROM isochrone_cache
 WHERE mode = 'transit' AND budget_mins IS NULL;

-- +goose Down
-- Only rows carrying a budget are deleted. They are the only rows that can
-- collide under the narrower key, and an old worker reading one would be
-- served a polygon cut for some other departure — the bug this undoes the fix
-- for. Walk/bike/drive rows are already unique without the column and keep
-- their warmth; 00024's Down had no such subset and cleared everything.
ALTER TABLE isochrone_cache DROP CONSTRAINT IF EXISTS isochrone_cache_key;
DELETE FROM isochrone_cache WHERE budget_mins IS NOT NULL;
ALTER TABLE isochrone_cache DROP COLUMN IF EXISTS budget_mins;
ALTER TABLE isochrone_cache
    ADD CONSTRAINT isochrone_cache_key
    UNIQUE NULLS NOT DISTINCT (compile_job_id, station_slug, mode, contour_mins, departs_on);
