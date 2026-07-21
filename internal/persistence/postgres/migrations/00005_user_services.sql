-- +goose Up
-- User-authored services (SPA-80).
--
-- Deliberately separate from `services`, the seeded aggregate that the physics
-- compiler consumes. A user service is self-contained: stops are embedded
-- points and vehicle params are inline, so authoring one touches no shared
-- catalog (no stations, no vehicle_types). Keeping it apart leaves the seeded
-- CAHSR compile path untouched.

CREATE TABLE user_services (
    id          uuid PRIMARY KEY,
    slug        text NOT NULL UNIQUE,
    route_id    uuid NOT NULL REFERENCES routes (id) ON DELETE CASCADE,
    -- Every user service has an owner; ownership is what gates writes.
    owner_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    -- Inline vehicle params: {max_speed_kmh, acceleration_ms2, deceleration_ms2, dwell_s}.
    vehicle     jsonb NOT NULL,
    -- Ordered embedded stops: [{name, lat, lng, seq}, ...]. Stored as a single
    -- document because a stop has no identity outside its service and the whole
    -- pattern is always read and rewritten together.
    stops       jsonb NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX user_services_owner_id_idx ON user_services (owner_id);
CREATE INDEX user_services_route_id_idx ON user_services (route_id);

-- Frequency windows are a real table rather than another jsonb column: they are
-- queried on their own when compiling headways into run-times.
CREATE TABLE user_service_frequency_windows (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_service_id uuid NOT NULL REFERENCES user_services (id) ON DELETE CASCADE,
    seq             integer NOT NULL,
    start_time      text NOT NULL,
    end_time        text NOT NULL,
    headway_s       integer NOT NULL,
    UNIQUE (user_service_id, seq)
);
CREATE INDEX user_service_frequency_windows_service_idx
    ON user_service_frequency_windows (user_service_id);

-- +goose Down
DROP TABLE IF EXISTS user_service_frequency_windows;
DROP TABLE IF EXISTS user_services;
