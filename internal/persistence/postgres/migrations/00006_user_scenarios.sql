-- +goose Up
-- User-owned scenarios (SPA-81): a curated set of user_services IDs.
--
-- Deliberately separate from `scenarios`/`scenario_service`, which back the
-- seeded, publicly-compiled TransitGraph pipeline. A user_scenario never
-- enters that pipeline: it has no routes, stations, or FK-matched membership,
-- just an explicit many-to-many join to the services its owner chose.

CREATE TABLE user_scenarios (
    id          uuid PRIMARY KEY,
    slug        text NOT NULL UNIQUE,
    owner_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX user_scenarios_owner_id_idx ON user_scenarios (owner_id);

-- Curated membership: exactly the user_services an owner chose, independent
-- of any FK. No seq column — the set has no ordering, unlike an ordered stop
-- pattern or frequency-window list.
CREATE TABLE user_scenario_services (
    user_scenario_id uuid NOT NULL REFERENCES user_scenarios (id) ON DELETE CASCADE,
    user_service_id  uuid NOT NULL REFERENCES user_services (id) ON DELETE CASCADE,
    PRIMARY KEY (user_scenario_id, user_service_id)
);
CREATE INDEX user_scenario_services_scenario_idx ON user_scenario_services (user_scenario_id);

-- +goose Down
DROP TABLE IF EXISTS user_scenario_services;
DROP TABLE IF EXISTS user_scenarios;
