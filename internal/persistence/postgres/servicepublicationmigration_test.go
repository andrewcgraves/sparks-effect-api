package postgres_test

import (
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

func rewindServicePublicationMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url, `DROP TABLE IF EXISTS service_publications`)
	rewindTo(t, url, 26)
}

func TestServicePublicationMigrationLeavesExistingServicesUnpublished(t *testing.T) {
	repo, ctx, url := userServiceFixture(t)
	svc := sampleUserService()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}

	rewindServicePublicationMigration(t, url)
	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got := scalarCount(t, url,
		`SELECT count(*) FROM service_publications WHERE user_service_id = '`+svc.ID+`'`); got != 0 {
		t.Fatalf("publications for a pre-existing service = %d, want 0", got)
	}
}

func TestServicePublicationMigrationIsSafeToReRun(t *testing.T) {
	_, ctx, url := userServiceFixture(t)

	rewindTo(t, url, 26)
	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatalf("re-running 00026 over the table it already created: %v", err)
	}
	if got := scalarCount(t, url,
		`SELECT count(*) FROM information_schema.tables
		  WHERE table_name = 'service_publications'`); got != 1 {
		t.Fatalf("service_publications tables = %d, want 1", got)
	}
}
