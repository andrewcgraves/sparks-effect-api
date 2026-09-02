package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

// rewindOwnedDomainModelsMigration unwinds 00022, unwinding the migration above
// it first the way every link in this chain does. 00023 and 00024 sit above it,
// so the tail of the rewind chain that starts in snapmigration_test.go is now
// rewindIsochroneCacheDepartsOnMigration. Goose refuses to re-apply a migration
// older than the highest version recorded, so anything rewinding a migration
// below this one must unrecord those — rewindTravelModeTransitMigration does
// that by calling this.
//
// It genuinely undoes the migration rather than merely unrecording it, for
// 00020's reason: every statement in 00022 is idempotent (IF NOT EXISTS on the
// columns and indexes, DROP CONSTRAINT IF EXISTS before each FK), so a bare
// DELETE from goose_db_version would leave the schema in place and the next
// Migrate would sail through as a no-op — hiding a rewind that did not actually
// rewind. The FK swap is undone too, since restoring the columns without
// restoring ON DELETE SET NULL would leave the database in a state neither
// migration produces.
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
		`ALTER TABLE routes DROP COLUMN IF EXISTS owner_id`,
		`DELETE FROM goose_db_version WHERE version_id = 22`)
}

// The ownership columns are what every curated-vs-owned read filters on, so
// their absence would not fail loudly — it would quietly publish user content.
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

// Both access patterns run against these indexes: owner_id = $1 for the
// owner-scoped lists, and owner_id IS NULL for the curated reads every boot
// performs. A partial index would serve only the first.
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

// The owner FKs must cascade, not null out. Under this design an unowned row is
// curated and public, so ON DELETE SET NULL would promote a deleted user's
// private scenario into the public compiled store — the exact leak the
// ownership filter exists to prevent.
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

// Every existing row predates ownership, so it must read back as curated.
// A backfill that assigned an owner would hide the ca-hsr baseline from the
// public reads that serve it.
func TestOwnedDomainModelsMigrationLeavesExistingRowsCurated(t *testing.T) {
	_, url := freshRepo(t)

	for _, table := range []string{"routes", "stations", "scenarios", "services"} {
		if got := scalarCount(t, url,
			`SELECT count(*) FROM `+table+` WHERE owner_id IS NOT NULL`); got != 0 {
			t.Errorf("%s: want every pre-existing row unowned, got %d owned", table, got)
		}
	}
}

// A schema change re-applied over a database that already holds it must not
// fail — the property every migration in this package is held to, and the
// reason 00022 is written with IF NOT EXISTS throughout.
//
// The unrecord here is deliberately bare rather than a call to the rewind
// helper above: that helper undoes the schema, which would make this a test
// that the migration applies from scratch. What is under test is the other
// case — Migrate meeting its own columns, indexes, and constraints already in
// place, as it does on any redeployed database.
func TestOwnedDomainModelsMigrationIsSafeToReRun(t *testing.T) {
	_, url := freshRepo(t)

	// 00023 and 00024 are unrecorded too: goose applies only versions above the
	// highest one recorded, so leaving either would make this re-run skip 00022
	// and prove nothing. Both are re-runnable over the schema they already wrote.
	exec(t, url, `DELETE FROM goose_db_version WHERE version_id IN (22, 23, 24)`)
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

// The rewind helper is a chain link every other rewinding test depends on, so
// its correctness is load-bearing: if it leaves the schema behind, the tests
// below it silently stop testing a rewind at all.
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
