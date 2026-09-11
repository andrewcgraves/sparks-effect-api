package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

func rewindOwnedDomainModelsMigration(t *testing.T, url string) {
	t.Helper()
	rewindCAHSRRoutingAnchorsMigration(t, url)
	exec(t, url,
		`ALTER TABLE services DROP CONSTRAINT IF EXISTS services_owner_id_fkey`,
		`ALTER TABLE services ADD CONSTRAINT services_owner_id_fkey
		   FOREIGN KEY (owner_id) REFERENCES users (id) ON DELETE SET NULL`,
		`ALTER TABLE scenarios DROP CONSTRAINT IF EXISTS scenarios_owner_id_fkey`,
		`ALTER TABLE scenarios ADD CONSTRAINT scenarios_owner_id_fkey
		   FOREIGN KEY (owner_id) REFERENCES users (id) ON DELETE SET NULL`,
		`DROP INDEX IF EXISTS services_owner_id_idx`,
		`DROP INDEX IF EXISTS scenarios_owner_id_idx`,
		`DROP INDEX IF EXISTS stations_owner_id_idx`,
		`DROP INDEX IF EXISTS routes_owner_id_idx`,
		`ALTER TABLE stations DROP COLUMN IF EXISTS owner_id`,
		`ALTER TABLE routes DROP COLUMN IF EXISTS description`,
		`ALTER TABLE routes DROP COLUMN IF EXISTS owner_id`)
	rewindTo(t, url, 22)
}

func TestOwnedDomainModelsMigrationAddsOwnershipColumns(t *testing.T) {
	_, url := freshRepo(t)

	for _, c := range []struct{ table, column string }{
		{"routes", "owner_id"},
		{"routes", "description"},
		{"stations", "owner_id"},
		{"scenarios", "owner_id"},
		{"services", "owner_id"},
	} {
		got := scalarCount(t, url,
			`SELECT count(*) FROM information_schema.columns
			  WHERE table_name = '`+c.table+`' AND column_name = '`+c.column+`'`)
		if got != 1 {
			t.Errorf("%s.%s: want the column to exist, got count %d", c.table, c.column, got)
		}
	}
}

func TestOwnedDomainModelsMigrationIndexesOwnership(t *testing.T) {
	_, url := freshRepo(t)

	for _, name := range []string{
		"routes_owner_id_idx",
		"stations_owner_id_idx",
		"scenarios_owner_id_idx",
		"services_owner_id_idx",
	} {
		if got := scalarCount(t, url,
			`SELECT count(*) FROM pg_indexes WHERE indexname = '`+name+`'`); got != 1 {
			t.Errorf("%s: want the index to exist, got count %d", name, got)
		}
	}
}

func TestOwnedDomainModelsMigrationCascadesOwnerDeletes(t *testing.T) {
	_, url := freshRepo(t)

	for _, table := range []string{"routes", "stations", "scenarios", "services"} {
		got := scalarCount(t, url,
			`SELECT count(*)
			   FROM information_schema.referential_constraints rc
			   JOIN information_schema.table_constraints tc
			     ON tc.constraint_name = rc.constraint_name
			  WHERE tc.table_name = '`+table+`'
			    AND tc.constraint_name = '`+table+`_owner_id_fkey'
			    AND rc.delete_rule = 'CASCADE'`)
		if got != 1 {
			t.Errorf("%s.owner_id: want ON DELETE CASCADE, got count %d", table, got)
		}
	}
}

func TestOwnedDomainModelsMigrationLeavesExistingRowsCurated(t *testing.T) {
	_, url := freshRepo(t)

	for _, table := range []string{"routes", "stations", "scenarios", "services"} {
		if got := scalarCount(t, url,
			`SELECT count(*) FROM `+table+` WHERE owner_id IS NOT NULL`); got != 0 {
			t.Errorf("%s: want every pre-existing row unowned, got %d owned", table, got)
		}
	}
}

func TestOwnedDomainModelsMigrationIsSafeToReRun(t *testing.T) {
	_, url := freshRepo(t)

	// Later versions are unrecorded too: goose applies only versions above the
	// highest one recorded, so leaving any would make this re-run skip 00022.
	rewindTo(t, url, 22)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("re-running 00022 over the schema it already created: %v", err)
	}

	if got := scalarCount(t, url,
		`SELECT count(*) FROM pg_indexes WHERE indexname = 'routes_owner_id_idx'`); got != 1 {
		t.Errorf("routes_owner_id_idx after a re-run: want 1, got %d", got)
	}
	// And the FK swap survived the second pass rather than being left half done.
	if got := scalarCount(t, url,
		`SELECT count(*) FROM information_schema.referential_constraints rc
		   JOIN information_schema.table_constraints tc
		     ON tc.constraint_name = rc.constraint_name
		  WHERE tc.constraint_name = 'scenarios_owner_id_fkey'
		    AND rc.delete_rule = 'CASCADE'`); got != 1 {
		t.Errorf("scenarios_owner_id_fkey after a re-run: want ON DELETE CASCADE, got count %d", got)
	}
}

func TestOwnedDomainModelsRewindActuallyUndoesTheMigration(t *testing.T) {
	_, url := freshRepo(t)

	rewindOwnedDomainModelsMigration(t, url)

	if got := scalarCount(t, url,
		`SELECT count(*) FROM information_schema.columns
		  WHERE table_name = 'routes' AND column_name = 'owner_id'`); got != 0 {
		t.Errorf("routes.owner_id after a rewind: want it gone, got count %d", got)
	}

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("Migrate after a full rewind: %v", err)
	}
	if got := scalarCount(t, url,
		`SELECT count(*) FROM information_schema.columns
		  WHERE table_name = 'routes' AND column_name = 'owner_id'`); got != 1 {
		t.Errorf("routes.owner_id after re-migrating: want it back, got count %d", got)
	}
}
