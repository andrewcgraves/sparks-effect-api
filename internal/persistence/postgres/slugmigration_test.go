package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

// The migration that gives every stop an identity (00008) chose to assert
// rather than backfill, the same way 00007 did: minting a slug in SQL would
// mean a second implementation of transit.Slugify and its disambiguation rule,
// and SPA-103 requires a stored slug to equal a derived one exactly. These
// tests pin that choice, so a later reader finds out what the migration does
// with legacy data from a test rather than from a production incident.

// rewindSlugMigration puts the database back to how it looked immediately
// before 00008 ran. goose then re-applies it on the next Migrate, which is what
// lets a test put a slugless row in front of the real migration file rather
// than in front of a copy of its SQL.
func rewindSlugMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`ALTER TABLE user_services DROP CONSTRAINT IF EXISTS user_services_stops_have_slugs`,
		`DELETE FROM goose_db_version WHERE version_id = 8`)
}

// insertPreSlugUserService writes a row in the post-snap, pre-SPA-103 shape:
// snapped coordinates, so 00007's constraint is satisfied, but no stop
// identities. It goes in through raw SQL because the Go model can no longer
// express a stop without one.
func insertPreSlugUserService(t *testing.T, url, stopsJSON string) {
	t.Helper()
	exec(t, url, `INSERT INTO user_services (id, slug, route_id, owner_id, name, vehicle, stops)
		VALUES ('`+usServiceID+`', 'legacy', '`+usRouteID+`', '`+usOwnerID+`', 'Legacy',
		        '{"max_speed_kmh":320,"acceleration_ms2":1.1,"deceleration_ms2":1.3,"dwell_s":45}',
		        '`+stopsJSON+`')`)
}

// TestSlugMigrationRefusesAPreSlugRow is the backfill test SPA-103 asks for.
// The chosen behaviour for a row whose stops have no identity is that the
// deploy stops and a human re-saves it, rather than SQL guessing at slugs the
// compiler would then derive differently.
func TestSlugMigrationRefusesAPreSlugRow(t *testing.T) {
	_, _, url := userServiceFixture(t)
	rewindSlugMigration(t, url)
	insertPreSlugUserService(t, url, `[
		{"name":"A","lat":37.0,"lng":-121.8,"seq":0,"chainage_m":0,"offset_m":0},
		{"name":"B","lat":37.0,"lng":-121.4,"seq":1,"chainage_m":35000,"offset_m":0}
	]`)

	err := postgres.Migrate(context.Background(), url)
	if err == nil {
		t.Fatal("migration accepted a row whose stops have no slug; it must refuse and let a human mint them")
	}
	for _, want := range []string{"user_services", "1 row"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("migration error %q does not mention %q", err, want)
		}
	}
}

// A present-but-empty slug is the case the constraint exists for: a missing key
// decodes to "", so without this the two would be indistinguishable and a stop
// with no identity would read as one whose identity is the empty string.
func TestSlugMigrationRefusesAnEmptySlug(t *testing.T) {
	_, _, url := userServiceFixture(t)
	rewindSlugMigration(t, url)
	insertPreSlugUserService(t, url, `[
		{"name":"A","slug":"legacy--a","lat":37.0,"lng":-121.8,"seq":0,"chainage_m":0,"offset_m":0},
		{"name":"B","slug":"","lat":37.0,"lng":-121.4,"seq":1,"chainage_m":35000,"offset_m":0}
	]`)

	if err := postgres.Migrate(context.Background(), url); err == nil {
		t.Fatal("migration accepted a stop whose slug is the empty string")
	}
}

// TestSlugMigrationRunsOnAnEmptyTable is the case actually expected in every
// environment: nothing to backfill, so the migration is just the invariant.
func TestSlugMigrationRunsOnAnEmptyTable(t *testing.T) {
	_, _, url := userServiceFixture(t)
	rewindSlugMigration(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration failed on an empty user_services: %v", err)
	}
}

// A row that already has identities is not legacy data, so the migration must
// pass it through rather than treat "was written before the constraint existed"
// as the thing it refuses.
func TestSlugMigrationAcceptsAnAlreadySluggedRow(t *testing.T) {
	_, _, url := userServiceFixture(t)
	rewindSlugMigration(t, url)
	insertPreSlugUserService(t, url, `[
		{"name":"A","slug":"legacy--a","lat":37.0,"lng":-121.8,"seq":0,"chainage_m":0,"offset_m":0},
		{"name":"B","slug":"legacy--b","lat":37.0,"lng":-121.4,"seq":1,"chainage_m":35000,"offset_m":0}
	]`)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration refused a row whose stops already have slugs: %v", err)
	}
}

// TestStopSlugConstraintRejectsASluglessStop covers the invariant the migration
// leaves behind, for writes that come after it.
func TestStopSlugConstraintRejectsASluglessStop(t *testing.T) {
	_, _, url := userServiceFixture(t)

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	_, err = conn.Exec(ctx, `INSERT INTO user_services (id, slug, route_id, owner_id, name, vehicle, stops)
		VALUES ('`+usServiceID+`', 'unslugged', '`+usRouteID+`', '`+usOwnerID+`', 'Unslugged',
		        '{"max_speed_kmh":320,"acceleration_ms2":1.1,"deceleration_ms2":1.3,"dwell_s":45}',
		        '[{"name":"A","slug":"unslugged--a","lat":37.0,"lng":-121.8,"seq":0,"chainage_m":0,"offset_m":0},
		          {"name":"B","lat":37.0,"lng":-121.4,"seq":1,"chainage_m":35000,"offset_m":0}]')`)
	if err == nil {
		t.Fatal("a stop with no slug was accepted")
	}
	if !strings.Contains(err.Error(), "user_services_stops_have_slugs") {
		t.Fatalf("want the stop-slug constraint to reject it, got %v", err)
	}
}

// TestStopSlugConstraintAcceptsWhatTheModelWrites guards against the constraint
// and the Go struct's json tags drifting apart: every stop the write path
// produces must satisfy it.
func TestStopSlugConstraintAcceptsWhatTheModelWrites(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)

	svc := sampleUserService()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService with minted stop slugs: %v", err)
	}
}
