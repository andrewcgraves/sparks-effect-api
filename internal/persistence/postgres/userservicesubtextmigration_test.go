package postgres_test

import (
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

func rewindUserServiceSubtextMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url, `ALTER TABLE user_services DROP COLUMN IF EXISTS subtext`)
	rewindTo(t, url, 25)
}

func userServicesHasSubtext(t *testing.T, url string) bool {
	t.Helper()
	return scalarCount(t, url,
		`SELECT count(*) FROM information_schema.columns
		  WHERE table_name = 'user_services' AND column_name = 'subtext'`) == 1
}

func TestUserServiceSubtextMigrationLeavesExistingRowsUnchanged(t *testing.T) {
	repo, ctx, url := userServiceFixture(t)

	// A service that exists before 00025: write it, then take the column away
	// so the row is in the shape a deployed database holds today.
	svc := sampleUserService()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}
	rewindUserServiceSubtextMigration(t, url)
	if userServicesHasSubtext(t, url) {
		t.Fatal("user_services.subtext survived the rewind")
	}

	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	got, found, err := repo.GetUserServiceByID(ctx, svc.ID)
	if err != nil || !found {
		t.Fatalf("GetUserServiceByID: found=%v err=%v", found, err)
	}
	if got.Subtext != "" {
		t.Errorf("subtext of a pre-existing row = %q, want empty", got.Subtext)
	}
	if got.Name != svc.Name || got.Description != svc.Description {
		t.Errorf("prose changed across the migration: name %q, description %q", got.Name, got.Description)
	}
}

func TestUserServiceSubtextMigrationIsSafeToReRun(t *testing.T) {
	repo, ctx, url := userServiceFixture(t)
	svc := sampleUserService()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}

	rewindTo(t, url, 25)
	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatalf("re-running 00025 over the column it already added: %v", err)
	}
	got, _, err := repo.GetUserServiceByID(ctx, svc.ID)
	if err != nil {
		t.Fatalf("GetUserServiceByID: %v", err)
	}
	if got.Subtext != svc.Subtext {
		t.Errorf("subtext after re-run = %q, want %q", got.Subtext, svc.Subtext)
	}
}
