package postgres_test

import (
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

func TestFirstPublishedAtMigrationBackfillsFromPublishedAt(t *testing.T) {
	repo, ctx, url := userServiceFixture(t)
	svc := indexedService(idxServiceA, "backfilled-line", "Backfilled Line")
	createIndexedServices(t, repo, ctx, svc)
	publishIndexed(t, repo, ctx, svc.ID, idxJobA)

	// A publication that exists before 00033: take the column away so the row
	// is in the shape a deployed database holds today.
	exec(t, url,
		`UPDATE service_publications SET published_at = '2001-05-01T00:00:00Z'`,
		`ALTER TABLE service_publications DROP COLUMN first_published_at`)
	rewindTo(t, url, 33)

	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got := scalarText(t, url,
		`SELECT first_published_at AT TIME ZONE 'UTC' || '' FROM service_publications`); got != "2001-05-01 00:00:00" {
		t.Fatalf("first_published_at = %q, want the publication's published_at", got)
	}
}

func TestFirstPublishedAtMigrationIsSafeToReRun(t *testing.T) {
	repo, ctx, url := userServiceFixture(t)
	svc := indexedService(idxServiceA, "rerun-line", "Rerun Line")
	createIndexedServices(t, repo, ctx, svc)
	publishIndexed(t, repo, ctx, svc.ID, idxJobA)
	pinFirstPublished(t, url, map[string]string{svc.ID: "2001-01-01T00:00:00Z"})

	rewindTo(t, url, 33)
	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatalf("re-running 00033 over the column it already added: %v", err)
	}
	// The backfill must not overwrite a first-publish time with a later
	// republish's published_at.
	if got := scalarText(t, url,
		`SELECT first_published_at AT TIME ZONE 'UTC' || '' FROM service_publications`); got != "2001-01-01 00:00:00" {
		t.Fatalf("first_published_at after re-run = %q, want it unchanged", got)
	}
	if got := scalarCount(t, url,
		`SELECT count(*) FROM pg_indexes
		  WHERE indexname = 'service_publications_first_published_at_idx'`); got != 1 {
		t.Fatalf("service_publications_first_published_at_idx indexes = %d, want 1", got)
	}
}
