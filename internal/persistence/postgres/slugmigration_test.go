package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/jackc/pgx/v5"
)

func rewindSlugMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`ALTER TABLE user_services DROP CONSTRAINT IF EXISTS user_services_stops_have_slugs`)
	rewindTo(t, url, 8)
	// 00009 sits after the migration under test, so it too must be unwound or
	// goose refuses to re-apply 00008 with a later version still recorded.
	rewindJobTargetsMigration(t, url)
}

func insertUserServiceRaw(t *testing.T, url, stopsJSON string) {
	t.Helper()
	exec(t, url, `INSERT INTO user_services (id, slug, route_id, owner_id, name, vehicle, stops)
		VALUES ('`+usServiceID+`', 'legacy', '`+usRouteID+`', '`+usOwnerID+`', 'Legacy',
		        '{"max_speed_kmh":320,"acceleration_ms2":1.1,"deceleration_ms2":1.3,"dwell_s":45}',
		        '`+stopsJSON+`')`)
}

func TestSlugMigrationRefusesAPreSlugRow(t *testing.T) {
	_, _, url := userServiceFixture(t)
	rewindSlugMigration(t, url)
	insertUserServiceRaw(t, url, `[
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

func TestSlugMigrationRefusesAnEmptySlug(t *testing.T) {
	_, _, url := userServiceFixture(t)
	rewindSlugMigration(t, url)
	insertUserServiceRaw(t, url, `[
		{"name":"A","slug":"legacy--a","lat":37.0,"lng":-121.8,"seq":0,"chainage_m":0,"offset_m":0},
		{"name":"B","slug":"","lat":37.0,"lng":-121.4,"seq":1,"chainage_m":35000,"offset_m":0}
	]`)

	if err := postgres.Migrate(context.Background(), url); err == nil {
		t.Fatal("migration accepted a stop whose slug is the empty string")
	}
}

func TestSlugMigrationRunsOnAnEmptyTable(t *testing.T) {
	_, _, url := userServiceFixture(t)
	rewindSlugMigration(t, url)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration failed on an empty user_services: %v", err)
	}
}

func TestSlugMigrationAcceptsAnAlreadySluggedRow(t *testing.T) {
	_, _, url := userServiceFixture(t)
	rewindSlugMigration(t, url)
	insertUserServiceRaw(t, url, `[
		{"name":"A","slug":"legacy--a","lat":37.0,"lng":-121.8,"seq":0,"chainage_m":0,"offset_m":0},
		{"name":"B","slug":"legacy--b","lat":37.0,"lng":-121.4,"seq":1,"chainage_m":35000,"offset_m":0}
	]`)

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration refused a row whose stops already have slugs: %v", err)
	}
}

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

func TestStopSlugConstraintAcceptsWhatTheModelWrites(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)

	svc := sampleUserService()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService with minted stop slugs: %v", err)
	}
}
