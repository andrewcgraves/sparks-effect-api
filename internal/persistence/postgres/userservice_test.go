package postgres_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const (
	usScenarioID = "00000000-0000-4001-8003-000000000001"
	usRouteID    = "00000000-0000-4002-8003-000000000001"
	usRouteID2   = "00000000-0000-4002-8003-000000000002"
	usOwnerID    = "00000000-0000-4007-8003-000000000001"
	usStrangerID = "00000000-0000-4007-8003-000000000002"
	usServiceID  = "00000000-0000-4008-8003-000000000001"
)

// userServiceFixture returns a repo pre-loaded with the rows a user service
// needs to exist: an owner, a scenario, and two routes.
func userServiceFixture(t *testing.T) (*postgres.Repo, context.Context, string) {
	t.Helper()
	ctx := context.Background()
	repo, url := freshRepo(t)

	for _, u := range []transit.User{
		{ID: usOwnerID, Email: "owner@example.com", Name: "Owner"},
		{ID: usStrangerID, Email: "stranger@example.com", Name: "Stranger"},
	} {
		if err := repo.CreateUser(ctx, u, ""); err != nil {
			t.Fatalf("CreateUser %s: %v", u.Email, err)
		}
	}
	if err := repo.CreateScenario(ctx, transit.Scenario{
		ID: usScenarioID, Slug: "us-net", Name: "User Service Net", Status: "draft",
	}); err != nil {
		t.Fatalf("CreateScenario: %v", err)
	}
	scenarioID := usScenarioID
	for i, id := range []string{usRouteID, usRouteID2} {
		if err := repo.CreateRoute(ctx, transit.Route{
			ID: id, ScenarioID: &scenarioID, Slug: fmt.Sprintf("us-route-%d", i),
			Name: "Route", Mode: "rail", Bidirectional: true,
			Geometry: transit.GeoLineString{
				Type: "LineString", Coordinates: [][]float64{{-122, float64(37 + i)}, {-121, float64(37 + i)}},
			},
		}); err != nil {
			t.Fatalf("CreateRoute %d: %v", i, err)
		}
	}
	return repo, ctx, url
}

func sampleUserService() transit.UserService {
	return transit.UserService{
		ID: usServiceID, Slug: "bay-area-express", RouteID: usRouteID, OwnerID: usOwnerID,
		Name: "Bay Area Express", Description: "Peak express",
		Vehicle: transit.VehicleParams{
			MaxSpeedKMH: 320, AccelerationMS2: 1.1, DecelerationMS2: 1.3, DwellS: 45,
		},
		Stops: []transit.ServiceStopPoint{
			{Name: "San Francisco", Lat: 37.7749, Lng: -122.4194, Seq: 0},
			{Name: "Millbrae", Lat: 37.5985, Lng: -122.3872, Seq: 1},
			{Name: "San Jose", Lat: 37.3382, Lng: -121.8863, Seq: 2},
		},
		FrequencyWindows: []transit.FrequencyWindow{
			{StartTime: "06:00", EndTime: "10:00", HeadwayS: 900},
			{StartTime: "10:00", EndTime: "16:00", HeadwayS: 1800},
		},
	}
}

func TestUserServiceRoundTrip(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	want := sampleUserService()

	if err := repo.CreateUserService(ctx, want); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}

	got, found, err := repo.GetUserServiceBySlug(ctx, want.Slug)
	if err != nil || !found {
		t.Fatalf("GetUserServiceBySlug: found=%v err=%v", found, err)
	}

	if got.ID != want.ID || got.Name != want.Name || got.Description != want.Description {
		t.Errorf("scalars: got %+v", got)
	}
	if got.RouteID != want.RouteID || got.OwnerID != want.OwnerID {
		t.Errorf("route/owner: got route=%s owner=%s", got.RouteID, got.OwnerID)
	}
	// Inline vehicle params survive the jsonb round-trip exactly.
	if got.Vehicle != want.Vehicle {
		t.Errorf("vehicle: got %+v, want %+v", got.Vehicle, want.Vehicle)
	}
	// Embedded stops come back in order.
	if len(got.Stops) != len(want.Stops) {
		t.Fatalf("got %d stops, want %d", len(got.Stops), len(want.Stops))
	}
	for i := range want.Stops {
		if got.Stops[i] != want.Stops[i] {
			t.Errorf("stop %d: got %+v, want %+v", i, got.Stops[i], want.Stops[i])
		}
	}
	// Frequency windows come back in the order they were written.
	if len(got.FrequencyWindows) != len(want.FrequencyWindows) {
		t.Fatalf("got %d windows, want %d", len(got.FrequencyWindows), len(want.FrequencyWindows))
	}
	for i := range want.FrequencyWindows {
		if got.FrequencyWindows[i] != want.FrequencyWindows[i] {
			t.Errorf("window %d: got %+v, want %+v", i, got.FrequencyWindows[i], want.FrequencyWindows[i])
		}
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("timestamps not populated: created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
	}
}

func TestGetUserServiceByIDMatchesBySlug(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	if err := repo.CreateUserService(ctx, sampleUserService()); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}

	byID, found, err := repo.GetUserServiceByID(ctx, usServiceID)
	if err != nil || !found {
		t.Fatalf("GetUserServiceByID: found=%v err=%v", found, err)
	}
	if byID.Slug != "bay-area-express" || len(byID.Stops) != 3 {
		t.Fatalf("got %+v", byID)
	}
}

func TestGetMissingUserServiceIsNotAnError(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)

	if _, found, err := repo.GetUserServiceBySlug(ctx, "nope"); err != nil || found {
		t.Fatalf("by slug: found=%v err=%v", found, err)
	}
	if _, found, err := repo.GetUserServiceByID(ctx, usServiceID); err != nil || found {
		t.Fatalf("by id: found=%v err=%v", found, err)
	}
}

func TestUpdateUserServiceReplacesAggregate(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	if err := repo.CreateUserService(ctx, sampleUserService()); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}

	updated := sampleUserService()
	updated.Name = "Renamed"
	updated.RouteID = usRouteID2
	updated.Vehicle.MaxSpeedKMH = 250
	updated.Vehicle.DwellS = 60
	// Shrink both collections — the old rows must not linger.
	updated.Stops = updated.Stops[:2]
	updated.FrequencyWindows = updated.FrequencyWindows[:1]

	if err := repo.UpdateUserService(ctx, updated); err != nil {
		t.Fatalf("UpdateUserService: %v", err)
	}

	got, _, err := repo.GetUserServiceByID(ctx, usServiceID)
	if err != nil {
		t.Fatalf("GetUserServiceByID: %v", err)
	}
	if got.Name != "Renamed" || got.RouteID != usRouteID2 {
		t.Errorf("scalars not updated: %+v", got)
	}
	if got.Vehicle.MaxSpeedKMH != 250 || got.Vehicle.DwellS != 60 {
		t.Errorf("vehicle not updated: %+v", got.Vehicle)
	}
	if len(got.Stops) != 2 {
		t.Errorf("got %d stops, want 2 — stale stops survived", len(got.Stops))
	}
	if len(got.FrequencyWindows) != 1 {
		t.Errorf("got %d windows, want 1 — stale windows survived", len(got.FrequencyWindows))
	}
	// Slug and owner are server-owned and must be untouched by an update.
	if got.Slug != "bay-area-express" || got.OwnerID != usOwnerID {
		t.Errorf("identity changed: slug=%s owner=%s", got.Slug, got.OwnerID)
	}
}

func TestUpdateMissingUserServiceErrors(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	if err := repo.UpdateUserService(ctx, sampleUserService()); err == nil {
		t.Fatal("UpdateUserService on a missing row: want error, got nil")
	}
}

func TestDeleteUserServiceCascadesWindows(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	if err := repo.CreateUserService(ctx, sampleUserService()); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}
	if err := repo.DeleteUserService(ctx, usServiceID); err != nil {
		t.Fatalf("DeleteUserService: %v", err)
	}

	if _, found, _ := repo.GetUserServiceByID(ctx, usServiceID); found {
		t.Fatal("service still present after delete")
	}
	// Re-creating with the same ID proves the child rows cascaded — a leftover
	// frequency window would violate its foreign key.
	if err := repo.CreateUserService(ctx, sampleUserService()); err != nil {
		t.Fatalf("recreate after delete: %v", err)
	}
	got, _, err := repo.GetUserServiceByID(ctx, usServiceID)
	if err != nil {
		t.Fatalf("GetUserServiceByID: %v", err)
	}
	if len(got.FrequencyWindows) != 2 {
		t.Fatalf("got %d windows after recreate, want 2", len(got.FrequencyWindows))
	}
}

func TestListUserServicesByOwnerIsScoped(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)

	mine := sampleUserService()
	if err := repo.CreateUserService(ctx, mine); err != nil {
		t.Fatalf("create mine: %v", err)
	}
	theirs := sampleUserService()
	theirs.ID = "00000000-0000-4008-8003-000000000002"
	theirs.Slug = "theirs"
	theirs.OwnerID = usStrangerID
	if err := repo.CreateUserService(ctx, theirs); err != nil {
		t.Fatalf("create theirs: %v", err)
	}

	got, err := repo.ListUserServicesByOwner(ctx, usOwnerID)
	if err != nil {
		t.Fatalf("ListUserServicesByOwner: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d services, want 1", len(got))
	}
	if got[0].ID != mine.ID || len(got[0].Stops) != 3 || len(got[0].FrequencyWindows) != 2 {
		t.Fatalf("listed service not fully hydrated: %+v", got[0])
	}
}

func TestListUserServicesByOwnerEmptyIsNotNil(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	got, err := repo.ListUserServicesByOwner(ctx, usOwnerID)
	if err != nil {
		t.Fatalf("ListUserServicesByOwner: %v", err)
	}
	if got == nil {
		t.Fatal("got nil slice, want empty")
	}
}

func TestUserServiceSlugIsUnique(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	if err := repo.CreateUserService(ctx, sampleUserService()); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}

	clash := sampleUserService()
	clash.ID = "00000000-0000-4008-8003-000000000003"
	if err := repo.CreateUserService(ctx, clash); err == nil {
		t.Fatal("duplicate slug: want a unique-violation error, got nil")
	}
}

func TestCreateUserServiceRejectsUnknownRoute(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	svc := sampleUserService()
	svc.RouteID = "00000000-0000-4002-8003-0000000000ff"

	if err := repo.CreateUserService(ctx, svc); err == nil {
		t.Fatal("unknown route: want a foreign-key error, got nil")
	}
}

func TestRouteExists(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)

	if exists, err := repo.RouteExists(ctx, usRouteID); err != nil || !exists {
		t.Fatalf("known route: exists=%v err=%v", exists, err)
	}
	if exists, err := repo.RouteExists(ctx, "00000000-0000-4002-8003-0000000000ff"); err != nil || exists {
		t.Fatalf("unknown route: exists=%v err=%v", exists, err)
	}
}

func TestDeletingOwnerRemovesTheirServices(t *testing.T) {
	repo, ctx, url := userServiceFixture(t)
	if err := repo.CreateUserService(ctx, sampleUserService()); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}

	// owner_id is NOT NULL with ON DELETE CASCADE — a user service cannot be
	// orphaned the way a seeded service can.
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `DELETE FROM users WHERE id = $1`, usOwnerID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if _, found, _ := repo.GetUserServiceByID(ctx, usServiceID); found {
		t.Fatal("service survived its owner's deletion")
	}
}
