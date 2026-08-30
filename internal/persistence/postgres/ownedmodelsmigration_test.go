package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

// ownedDomainModelsFloor is the version 00021 sits directly above: rolling back
// to it is what unwinds 00021 and nothing below it.
const ownedDomainModelsFloor = 20

// rewindOwnedDomainModelsMigration unwinds 00021 and is the current tail of the
// rewind chain that starts in snapmigration_test.go. Any migration added after
// this one must extend the chain here, or every rewinding test in this package
// starts failing with goose's "missing migrations before current version".
//
// It genuinely undoes the migration rather than merely unrecording it, for
// 00020's reason: every statement in 00021 is idempotent (IF NOT EXISTS on the
// columns and indexes, DROP CONSTRAINT IF EXISTS before each FK), so a bare
// DELETE from goose_db_version would leave the schema in place and the next
// Migrate would sail through as a no-op — hiding a rewind that did not actually
// rewind.
//
// It undoes it by running 00021's own `-- +goose Down` block rather than by
// restating those statements in Go. A restatement is a twin: it compiles and
// passes whatever it says, so an edit to the migration's Down block leaves it
// silently disagreeing about what "before 00021" means, and this helper is what
// most of the package's rewinding tests stand on.
//
// Down-*to* rather than a single Down step because this helper is called more
// than once against the same database, sometimes when 00021 is already
// unrecorded — TestPrerenderedIsochronesMigrationIsSafeToReRun deliberately
// leaves it that way by letting a Migrate fail. goose.Down would then roll back
// whatever the current version happened to be, which is some unrelated earlier
// migration; DownTo compares against the fixed floor below and does nothing
// once the database is already at or under it. That is the same no-op the old
// hand-written `DELETE ... WHERE version_id = 21` gave, kept deliberately
// rather than by accident.
func rewindOwnedDomainModelsMigration(t *testing.T, url string) {
	t.Helper()
	if err := postgres.MigrateDownTo(context.Background(), url, ownedDomainModelsFloor); err != nil {
		t.Fatalf("rewinding 00021: %v", err)
	}
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
// reason 00021 is written with IF NOT EXISTS throughout.
//
// The unrecord here is deliberately bare rather than a call to the rewind
// helper above: that helper undoes the schema, which would make this a test
// that the migration applies from scratch. What is under test is the other
// case — Migrate meeting its own columns, indexes, and constraints already in
// place, as it does on any redeployed database.
func TestOwnedDomainModelsMigrationIsSafeToReRun(t *testing.T) {
	_, url := freshRepo(t)

	exec(t, url, `DELETE FROM goose_db_version WHERE version_id = 21`)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("re-running 00021 over the schema it already created: %v", err)
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
