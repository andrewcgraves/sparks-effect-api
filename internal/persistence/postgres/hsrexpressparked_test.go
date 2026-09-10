package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

const hsrExpressID = "00000000-0000-4004-8001-000000000001"

func rewindHSRExpressParkedMigration(t *testing.T, url string) {
	t.Helper()
	rewindSegmentReverseRunSecondsMigration(t, url)
	rewindTo(t, url, 17)
}

func insertPreFixHSRExpress(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`INSERT INTO scenarios (id, slug, name) VALUES ('`+phase1ScenarioID+`', 'ca-hsr', 'CA HSR')`,
		`INSERT INTO vehicle_types (id, name, max_speed_kmh, acceleration_ms2, deceleration_ms2, dwell_level_s, dwell_step_s)
		   VALUES ('00000000-0000-4003-8001-000000000001', 'HSR trainset', 350, 0.6, 0.8, 30, 10)`,
		`INSERT INTO routes (id, scenario_id, slug, name, geometry)
		   VALUES ('`+phase1RouteID+`', '`+phase1ScenarioID+`', 'ca-hsr-phase-1', 'CA HSR Phase 1',
		           '{"type":"LineString","coordinates":[[-121.9,37.3],[-118.2,34.0]]}'::jsonb)`,
		`INSERT INTO services (id, scenario_id, route_id, vehicle_type_id, name, direction, active, provenance)
		   VALUES ('`+hsrExpressID+`', '`+phase1ScenarioID+`', '`+phase1RouteID+`',
		           '00000000-0000-4003-8001-000000000001', 'HSR Express', 'both', true, 'calibrated')`)
}

func TestHSRExpressParkedMigrationCorrectsAnAlreadyPopulatedScenario(t *testing.T) {
	_, url := freshRepo(t)
	rewindHSRExpressParkedMigration(t, url)
	insertPreFixHSRExpress(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration failed over a seeded ca-hsr: %v", err)
	}

	if got := scalarCount(t, url,
		`SELECT count(*) FROM services WHERE id = '`+hsrExpressID+`' AND active = false`); got != 1 {
		t.Error("HSR Express was not parked (active = false) by the migration")
	}
}

func TestHSRExpressParkedMigrationIsANoOpOnAnEmptyDatabase(t *testing.T) {
	_, url := freshRepo(t)

	if got := scalarCount(t, url,
		`SELECT count(*) FROM services WHERE id = '`+hsrExpressID+`'`); got != 0 {
		t.Errorf("migration wrote an HSR Express service on an empty database: got %d", got)
	}
}

func TestHSRExpressParkedMigrationIsSafeToReRun(t *testing.T) {
	_, url := freshRepo(t)
	rewindHSRExpressParkedMigration(t, url)
	insertPreFixHSRExpress(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("first migration: %v", err)
	}

	// Forget that it ran while keeping the data it wrote, so the second pass
	// meets exactly the state a YAML-seeded database would present. 00019
	// goes through its rewind rather than rewindTo alone: it creates a table,
	// and leaving the table behind would fail the re-migrate on CREATE TABLE.
	rewindPrerenderedIsochronesMigration(t, url)
	rewindTo(t, url, 17)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration re-run over the data it already wrote: %v", err)
	}

	if got := scalarCount(t, url,
		`SELECT count(*) FROM services WHERE id = '`+hsrExpressID+`' AND active = false`); got != 1 {
		t.Error("HSR Express after a re-run: want parked (active = false)")
	}
}
