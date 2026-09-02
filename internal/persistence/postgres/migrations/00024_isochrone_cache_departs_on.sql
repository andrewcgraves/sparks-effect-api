-- +goose Up
-- Transit polygons are a function of the service date Valhalla was asked to
-- depart on, and that date was not in the cache key (SPA-269).
--
-- ## Why this column exists
--
-- The worker stamps every multimodal request with WeekdayDepartAt: 08:00 on
-- today-or-the-next-weekday. That date advances daily. Until now the cache was
-- keyed (compile_job_id, station_slug, mode, contour_mins) and Put used
-- ON CONFLICT DO NOTHING, so the first request froze that day's answer for as
-- long as the compile job lived. For the public seeded corridor that is
-- indefinite — CompileSeededIfNeeded only mints a new job when the seed rows
-- compile to different bytes.
--
-- Walk/bike/drive polygons do not take a service date, so the column is NULL
-- for those modes. Transit writes the date the request used. A Get for a
-- different date is a miss and is recomputed under the new date, which is what
-- makes GTFS calendar-boundary drift self-correcting instead of mixing
-- pre-expiry and post-expiry polygons in one response.
--
-- ## Why uniqueness changes, and why NULLS NOT DISTINCT
--
-- A PRIMARY KEY cannot contain NULLs, and Postgres UNIQUE treats NULLs as
-- distinct, so adding a nullable column to the original four-column primary
-- key would either refuse walk/bike/drive rows or allow two of them. The
-- replacement is a UNIQUE NULLS NOT DISTINCT constraint on the five columns:
-- two NULLs collide, so walk/bike/drive keep one row per (graph, station,
-- mode, contour); two real dates do not, so two service dates are two rows.
--
-- tileset_at stays off uniqueness. 00014 left it diagnostic-only because
-- keying on it would invalidate the whole cache on every tile rebuild; that
-- invalidation is now the correct outcome, but it happens on read in the
-- worker (a mismatched or NULL stamp is a miss), not by minting a new key.
--
-- ## Deploy order
--
-- The column must exist before the worker writes it. The worker's ON CONFLICT
-- target must move from the four-column primary key to this five-column unique
-- (or ON CONSTRAINT isochrone_cache_key) in the same window; an un-updated
-- worker will fail those INSERTs until it does. Cache Put failures degrade to
-- computing the call (SPA-186), so the window is noisy rather than broken.
--
-- ADD COLUMN IF NOT EXISTS, and DROP CONSTRAINT IF EXISTS before the unique
-- is re-added, for 00018's reason: a schema change re-run against data it
-- already wrote must not fail on "already exists". Dropping the original
-- primary key is IF EXISTS so a second pass, which already replaced it, is a
-- no-op on that statement.

ALTER TABLE isochrone_cache
    ADD COLUMN IF NOT EXISTS departs_on date;

ALTER TABLE isochrone_cache
    DROP CONSTRAINT IF EXISTS isochrone_cache_pkey;

ALTER TABLE isochrone_cache
    DROP CONSTRAINT IF EXISTS isochrone_cache_key;

ALTER TABLE isochrone_cache
    ADD CONSTRAINT isochrone_cache_key
    UNIQUE NULLS NOT DISTINCT (compile_job_id, station_slug, mode, contour_mins, departs_on);

-- +goose Down
ALTER TABLE isochrone_cache DROP CONSTRAINT IF EXISTS isochrone_cache_key;
DELETE FROM isochrone_cache;
ALTER TABLE isochrone_cache DROP COLUMN IF EXISTS departs_on;
ALTER TABLE isochrone_cache DROP CONSTRAINT IF EXISTS isochrone_cache_pkey;
ALTER TABLE isochrone_cache
    ADD PRIMARY KEY (compile_job_id, station_slug, mode, contour_mins);
