package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func scenariosHasStatus(t *testing.T, url string) bool {
	t.Helper()
	return scalarCount(t, url,
		`SELECT count(*) FROM information_schema.columns
		  WHERE table_name = 'scenarios' AND column_name = 'status'`) == 1
}

// rewindRetireScenarioStatusMigration puts the column back as a deployed
// database holds it before 00028, value and all, and unrecords 00028.
func rewindRetireScenarioStatusMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`ALTER TABLE scenarios ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT ''`,
		`UPDATE scenarios SET status = 'published' WHERE slug = 'ca-hsr'`)
	rewindTo(t, url, 28)
}

func TestRetireScenarioStatusMigrationDropsTheColumn(t *testing.T) {
	_, url := freshRepo(t)
	if scenariosHasStatus(t, url) {
		t.Fatal("scenarios.status exists on a migrated database")
	}
}

func TestRetireScenarioStatusMigrationLeavesSeedReconciliationANoOp(t *testing.T) {
	repo, url := freshRepo(t)
	ctx := context.Background()

	if _, err := transit.SeedIfEmpty(ctx, repo); err != nil {
		t.Fatalf("SeedIfEmpty: %v", err)
	}
	rewindRetireScenarioStatusMigration(t, url)
	if !scenariosHasStatus(t, url) {
		t.Fatal("scenarios.status did not come back for the rewind")
	}

	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if scenariosHasStatus(t, url) {
		t.Fatal("scenarios.status survived 00028")
	}

	written, err := transit.ReconcileSeed(ctx, repo)
	if err != nil {
		t.Fatalf("ReconcileSeed: %v", err)
	}
	if written != 0 {
		t.Fatalf("ReconcileSeed wrote %d rows on an already-reconciled database, want 0", written)
	}
}

func TestRetireScenarioStatusMigrationIsSafeToReRun(t *testing.T) {
	_, url := freshRepo(t)
	ctx := context.Background()

	rewindTo(t, url, 28)
	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatalf("re-running 00028 over the column it already dropped: %v", err)
	}
	if scenariosHasStatus(t, url) {
		t.Fatal("scenarios.status reappeared after re-running 00028")
	}
}
