-- +goose Up
-- Initial schema for the writable persistence layer (SPA-73).
-- Native Postgres types throughout: uuid PKs, timestamptz, jsonb geometry,
-- real booleans, natively-enforced foreign keys. Geometry is GeoJSON in jsonb
-- (PostGIS deliberately deferred).

CREATE TABLE users (
    id         uuid PRIMARY KEY,
    email      text NOT NULL UNIQUE,
    name       text NOT NULL DEFAULT '',
    is_admin   boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE vehicle_types (
    id               uuid PRIMARY KEY,
    name             text NOT NULL,
    propulsion       text NOT NULL DEFAULT '',
    max_speed_kmh    double precision NOT NULL,
    acceleration_ms2 double precision NOT NULL,
    deceleration_ms2 double precision NOT NULL,
    floor_height     text NOT NULL DEFAULT '',
    dwell_level_s    integer NOT NULL,
    dwell_step_s     integer NOT NULL
);

CREATE TABLE scenarios (
    id          uuid PRIMARY KEY,
    slug        text NOT NULL UNIQUE,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    status      text NOT NULL DEFAULT '',
    owner_id    uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE routes (
    id            uuid PRIMARY KEY,
    scenario_id   uuid NOT NULL REFERENCES scenarios (id) ON DELETE CASCADE,
    name          text NOT NULL,
    mode          text NOT NULL DEFAULT '',
    geometry      jsonb NOT NULL,
    bidirectional boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX routes_scenario_id_idx ON routes (scenario_id);

CREATE TABLE stations (
    id              uuid PRIMARY KEY,
    scenario_id     uuid NOT NULL REFERENCES scenarios (id) ON DELETE CASCADE,
    slug            text NOT NULL,
    name            text NOT NULL,
    location        jsonb NOT NULL,
    platform_height text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (scenario_id, slug)
);

CREATE TABLE services (
    id              uuid PRIMARY KEY,
    scenario_id     uuid NOT NULL REFERENCES scenarios (id) ON DELETE CASCADE,
    route_id        uuid NOT NULL REFERENCES routes (id),
    vehicle_type_id uuid NOT NULL REFERENCES vehicle_types (id),
    name            text NOT NULL,
    direction       text NOT NULL DEFAULT '',
    active          boolean NOT NULL DEFAULT true,
    provenance      text NOT NULL DEFAULT '',
    owner_id        uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX services_scenario_id_idx ON services (scenario_id);

CREATE TABLE service_stops (
    service_id uuid NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    station_id uuid NOT NULL REFERENCES stations (id),
    sequence   integer NOT NULL,
    dwell_s    integer,
    PRIMARY KEY (service_id, sequence)
);

CREATE TABLE frequency_windows (
    id         uuid PRIMARY KEY,
    service_id uuid NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    start_time text NOT NULL,
    end_time   text NOT NULL,
    headway_s  integer NOT NULL
);
CREATE INDEX frequency_windows_service_id_idx ON frequency_windows (service_id);

-- Curated membership: which services a scenario exposes, independent of the
-- ownership FK on services.scenario_id.
CREATE TABLE scenario_service (
    scenario_id uuid NOT NULL REFERENCES scenarios (id) ON DELETE CASCADE,
    service_id  uuid NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    PRIMARY KEY (scenario_id, service_id)
);

CREATE TABLE travel_time_sets (
    scenario_id uuid PRIMARY KEY REFERENCES scenarios (id) ON DELETE CASCADE,
    provenance  text NOT NULL DEFAULT '',
    source      text NOT NULL DEFAULT ''
);

CREATE TABLE segments (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    scenario_id uuid NOT NULL REFERENCES scenarios (id) ON DELETE CASCADE,
    from_slug   text NOT NULL,
    to_slug     text NOT NULL,
    run_seconds integer NOT NULL
);
CREATE INDEX segments_scenario_id_idx ON segments (scenario_id);

CREATE TABLE jobs (
    id          uuid PRIMARY KEY,
    kind        text NOT NULL,
    status      text NOT NULL,
    scenario_id uuid REFERENCES scenarios (id) ON DELETE SET NULL,
    owner_id    uuid REFERENCES users (id) ON DELETE SET NULL,
    error       text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS segments;
DROP TABLE IF EXISTS travel_time_sets;
DROP TABLE IF EXISTS scenario_service;
DROP TABLE IF EXISTS frequency_windows;
DROP TABLE IF EXISTS service_stops;
DROP TABLE IF EXISTS services;
DROP TABLE IF EXISTS stations;
DROP TABLE IF EXISTS routes;
DROP TABLE IF EXISTS scenarios;
DROP TABLE IF EXISTS vehicle_types;
DROP TABLE IF EXISTS users;
