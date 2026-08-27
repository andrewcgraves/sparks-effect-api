package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

// rewindOwnedDomainModelsMigration unwinds 00020 and is the current tail of the
// rewind chain that starts in snapmigration_test.go. Any migration added after
// this one must extend the chain here, or every rewinding test in this package
// starts failing with goose's "missing migrations before current version".
//
// Unlike 00019's link, this one is a bare unrecord: every statement in 00020 is
// idempotent (IF NOT EXISTS on the columns and indexes, DROP CONSTRAINT IF
// EXISTS before each FK), so the next Migrate re-applies it over the schema it
// already created. Writing a hand-rolled undo here instead would be a second
// copy of the migration that nothing keeps in step with the first.
func rewindOwnedDomainModelsMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url, `DELETE FROM goose_db_version WHERE version_id = 20`)
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

// 00020 is re-applied over its own schema by every rewinding test in this
// package, so its idempotency is load-bearing rather than incidental.
func TestOwnedDomainModelsMigrationIsSafeToReRun(t *testing.T) {
	_, url := freshRepo(t)

	rewindOwnedDomainModelsMigration(t, url)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("re-running 00020 over the schema it already created: %v", err)
	}

	if got := scalarCount(t, url,
		`SELECT count(*) FROM pg_indexes WHERE indexname = 'routes_owner_id_idx'`); got != 1 {
		t.Errorf("routes_owner_id_idx after a re-run: want 1, got %d", got)
	}
}
