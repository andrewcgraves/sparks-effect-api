package postgres_test

import (
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

func TestPublishedAtIndexMigrationIndexesThePublishTime(t *testing.T) {
	_, url := freshRepo(t)

	def := scalarText(t, url,
		`SELECT indexdef FROM pg_indexes
		  WHERE tablename = 'service_publications'
		    AND indexname = 'service_publications_published_at_idx'`)
	if !strings.Contains(def, "(published_at DESC)") {
		t.Fatalf("indexdef = %q, want an index on published_at DESC", def)
	}
}

func TestPublishedAtIndexMigrationIsSafeToReRun(t *testing.T) {
	_, ctx, url := userServiceFixture(t)

	rewindTo(t, url, 27)
	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatalf("re-running 00027 over the index it already created: %v", err)
	}
	if got := scalarCount(t, url,
		`SELECT count(*) FROM pg_indexes
		  WHERE indexname = 'service_publications_published_at_idx'`); got != 1 {
		t.Fatalf("service_publications_published_at_idx indexes = %d, want 1", got)
	}
}
