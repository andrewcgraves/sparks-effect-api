-- +goose Up
-- Curated, ready-to-display isochrone payloads, scoped to a scenario.
--
-- ## What this is for
--
-- An isochrone normally costs a routing job: the API resolves a compiled
-- graph, publishes it, and a worker plots it against Valhalla (00014). That is
-- the right shape for a pin someone just dropped, and the wrong shape for the
-- handful of illustrative isochrones a scenario's page wants to show the
-- moment it loads. Those are authored once and shown forever, so they are
-- stored already computed and served as-is.
--
-- ## Why scenario_slug and not scenario_id
--
-- The public isochrone surface keys on scenario_slug throughout — the request
-- body names one (handler.Isochrone), and the calibrated run times are
-- addressed by it (travel_time_sets/GetTravelTimes). Keying these rows the same
-- way means the read path that serves them needs no id translation it does not
-- already do. scenarios.slug is UNIQUE (00001), so this is a real foreign key
-- and not a soft reference; CASCADE because a prerendered isochrone for a
-- scenario that no longer exists is unreachable garbage.
--
-- ## result is opaque
--
-- Exactly like routing_jobs.result: this repository neither produces nor
-- interprets an isochrone payload, so there is no shape to validate here and
-- no Go type it is checked against. It is written by whoever curated it and
-- handed back to the client byte for byte. Payloads run 300-500KB, which is
-- why every list read deliberately selects the other columns only.
--
-- ## compiled_service_ids
--
-- The same service-membership snapshot jobs.compiled_service_ids holds
-- (00009), taken when the entry was curated. Staleness is computed on read by
-- comparing it against the scenario's live membership (transit.MembershipStale)
-- — there is no stored flag and no trigger, because the comparison is cheap
-- and a persisted flag would have to be invalidated by everything that can
-- change a service. A stale entry is never hidden or refused; it is reported
-- as "outdated": true and still served.
--
-- A plain uuid[] rather than an FK array, for 00009's reason: it is a snapshot,
-- so a since-deleted member must stay listed, which a CASCADE FK would defeat.
-- DEFAULT '{}' rather than NULL so the "nothing was compiled" and "we never
-- recorded it" cases are not spelled two ways.

CREATE TABLE prerendered_isochrones (
    id            uuid PRIMARY KEY,
    scenario_slug text NOT NULL REFERENCES scenarios (slug) ON DELETE CASCADE,
    -- What this isochrone is called on the page that shows it.
    label       text NOT NULL,
    lat         double precision NOT NULL,
    lng         double precision NOT NULL,
    budget_mins integer NOT NULL,
    -- The domain's own vocabulary: walk / bike / drive (transit.TravelMode),
    -- the same spelling routing_jobs.mode stores.
    mode text NOT NULL,
    -- Opaque to this repository; see above.
    result               jsonb  NOT NULL,
    compiled_service_ids uuid[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- The list read is always "everything for this scenario, in creation order",
-- which is the only access path besides the primary key.
CREATE INDEX prerendered_isochrones_scenario_slug_idx
    ON prerendered_isochrones (scenario_slug);

-- +goose Down
DROP TABLE IF EXISTS prerendered_isochrones;
