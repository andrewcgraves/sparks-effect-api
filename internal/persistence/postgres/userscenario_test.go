package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const (
	usnScenarioID  = "00000000-0000-4001-8006-000000000001"
	usnRouteID     = "00000000-0000-4002-8006-000000000001"
	usnOwnerID     = "00000000-0000-4007-8006-000000000001"
	usnStrangerID  = "00000000-0000-4007-8006-000000000002"
	usnService1ID  = "00000000-0000-4008-8006-000000000001"
	usnService2ID  = "00000000-0000-4008-8006-000000000002"
	usnScenario1ID = "00000000-0000-4009-8006-000000000001"
)

// userScenarioFixture returns a repo pre-loaded with an owner, a stranger, a
// route, and two user services owned by the owner — the rows a user scenario
// needs to reference.
func userScenarioFixture(t *testing.T) (*postgres.Repo, context.Context) {
	t.Helper()
	ctx := context.Background()
	repo, _ := freshRepo(t)

	for _, u := range []transit.User{
		{ID: usnOwnerID, Email: "usn-owner@example.com", Name: "Owner"},
		{ID: usnStrangerID, Email: "usn-stranger@example.com", Name: "Stranger"},
	} {
		if err := repo.CreateUser(ctx, u, ""); err != nil {
			t.Fatalf("CreateUser %s: %v", u.Email, err)
		}
	}
	if err := repo.CreateScenario(ctx, transit.Scenario{
		ID: usnScenarioID, Slug: "usn-net", Name: "User Scenario Net", Status: "draft",
	}); err != nil {
		t.Fatalf("CreateScenario: %v", err)
	}
	if err := repo.CreateRoute(ctx, transit.Route{
		ID: usnRouteID, ScenarioID: ptr(usnScenarioID), Slug: "usn-route", Name: "Route",
		Mode: "rail", Bidirectional: true,
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122, 37}, {-121, 37}}},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}

	basicSvc := func(id, slug, owner string) transit.UserService {
		svc := transit.UserService{
			ID: id, Slug: slug, RouteID: usnRouteID, OwnerID: owner, Name: "Service " + slug,
			Vehicle: transit.VehicleParams{MaxSpeedKMH: 200, AccelerationMS2: 1, DecelerationMS2: 1, DwellS: 30},
			Stops: []transit.ServiceStopPoint{
				{Name: "A", Lat: 1, Lng: 1, Seq: 0},
				{Name: "B", Lat: 2, Lng: 2, Seq: 1},
			},
		}
		svc.MintStopSlugs()
		return svc
	}
	if err := repo.CreateUserService(ctx, basicSvc(usnService1ID, "usn-service-1", usnOwnerID)); err != nil {
		t.Fatalf("CreateUserService 1: %v", err)
	}
	if err := repo.CreateUserService(ctx, basicSvc(usnService2ID, "usn-service-2", usnOwnerID)); err != nil {
		t.Fatalf("CreateUserService 2: %v", err)
	}
	return repo, ctx
}

func sampleUserScenario() transit.UserScenario {
	return transit.UserScenario{
		ID: usnScenario1ID, Slug: "weekend-getaway", OwnerID: usnOwnerID,
		Name: "Weekend Getaway", Description: "Fri-Sun services",
		ServiceIDs: []string{usnService1ID, usnService2ID},
	}
}

func TestUserScenarioRoundTrip(t *testing.T) {
	repo, ctx := userScenarioFixture(t)
	want := sampleUserScenario()

	if err := repo.CreateUserScenario(ctx, want); err != nil {
		t.Fatalf("CreateUserScenario: %v", err)
	}

	got, found, err := repo.GetUserScenarioBySlug(ctx, want.Slug)
	if err != nil || !found {
		t.Fatalf("GetUserScenarioBySlug: found=%v err=%v", found, err)
	}
	if got.ID != want.ID || got.Name != want.Name || got.Description != want.Description {
		t.Errorf("scalar fields did not round-trip: got %+v", got)
	}
	if got.OwnerID != usnOwnerID {
		t.Errorf("owner_id = %q, want %q", got.OwnerID, usnOwnerID)
	}
	if len(got.ServiceIDs) != 2 {
		t.Fatalf("service_ids: want 2, got %d (%v)", len(got.ServiceIDs), got.ServiceIDs)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("timestamps not populated")
	}

	byID, found, err := repo.GetUserScenarioByID(ctx, want.ID)
	if err != nil || !found {
		t.Fatalf("GetUserScenarioByID: found=%v err=%v", found, err)
	}
	if byID.Slug != want.Slug {
		t.Errorf("GetUserScenarioByID: got slug %q, want %q", byID.Slug, want.Slug)
	}
}

// SPA-120: a scenario's declared interchange pairs round-trip through the
// jsonb column exactly as authored.
func TestUserScenarioRoundTripInterchangePairs(t *testing.T) {
	repo, ctx := userScenarioFixture(t)
	want := sampleUserScenario()
	want.InterchangePairs = []transit.InterchangePair{
		{A: transit.StopIdentity{ServiceID: usnService1ID, Slug: "usn-service-1--a"},
			B: transit.StopIdentity{ServiceID: usnService2ID, Slug: "usn-service-2--a"}},
	}

	if err := repo.CreateUserScenario(ctx, want); err != nil {
		t.Fatalf("CreateUserScenario: %v", err)
	}

	got, found, err := repo.GetUserScenarioBySlug(ctx, want.Slug)
	if err != nil || !found {
		t.Fatalf("GetUserScenarioBySlug: found=%v err=%v", found, err)
	}
	if len(got.InterchangePairs) != 1 || got.InterchangePairs[0] != want.InterchangePairs[0] {
		t.Fatalf("interchange_pairs = %+v, want %+v", got.InterchangePairs, want.InterchangePairs)
	}
}

func TestUserScenarioUnknownSlugAndID(t *testing.T) {
	repo, ctx := userScenarioFixture(t)

	if _, found, err := repo.GetUserScenarioBySlug(ctx, "no-such-slug"); found || err != nil {
		t.Errorf("unknown slug: found=%v err=%v, want false/nil", found, err)
	}
	if _, found, err := repo.GetUserScenarioByID(ctx, "00000000-0000-4009-8006-0000000000ff"); found || err != nil {
		t.Errorf("unknown id: found=%v err=%v, want false/nil", found, err)
	}
}

func TestUserScenarioSlugIsUnique(t *testing.T) {
	repo, ctx := userScenarioFixture(t)
	sc := sampleUserScenario()
	if err := repo.CreateUserScenario(ctx, sc); err != nil {
		t.Fatalf("CreateUserScenario: %v", err)
	}

	dup := sc
	dup.ID = "00000000-0000-4009-8006-000000000002"
	if err := repo.CreateUserScenario(ctx, dup); err == nil {
		t.Error("duplicate slug: want error, got nil")
	}
}

func TestUserScenarioUpdateReplacesMembership(t *testing.T) {
	repo, ctx := userScenarioFixture(t)
	sc := sampleUserScenario()
	sc.ServiceIDs = []string{usnService1ID}
	if err := repo.CreateUserScenario(ctx, sc); err != nil {
		t.Fatalf("CreateUserScenario: %v", err)
	}

	sc.Name = "Renamed"
	sc.Description = "New description"
	sc.ServiceIDs = []string{usnService2ID}
	if err := repo.UpdateUserScenario(ctx, sc); err != nil {
		t.Fatalf("UpdateUserScenario: %v", err)
	}

	got, found, err := repo.GetUserScenarioByID(ctx, sc.ID)
	if err != nil || !found {
		t.Fatalf("GetUserScenarioByID: found=%v err=%v", found, err)
	}
	if got.Name != "Renamed" || got.Description != "New description" {
		t.Errorf("scalar update did not apply: %+v", got)
	}
	if len(got.ServiceIDs) != 1 || got.ServiceIDs[0] != usnService2ID {
		t.Errorf("membership not replaced: %v", got.ServiceIDs)
	}
}

func TestUserScenarioUpdateToEmptyMembership(t *testing.T) {
	repo, ctx := userScenarioFixture(t)
	sc := sampleUserScenario()
	if err := repo.CreateUserScenario(ctx, sc); err != nil {
		t.Fatalf("CreateUserScenario: %v", err)
	}

	sc.ServiceIDs = nil
	if err := repo.UpdateUserScenario(ctx, sc); err != nil {
		t.Fatalf("UpdateUserScenario: %v", err)
	}

	got, _, err := repo.GetUserScenarioByID(ctx, sc.ID)
	if err != nil {
		t.Fatalf("GetUserScenarioByID: %v", err)
	}
	if len(got.ServiceIDs) != 0 {
		t.Errorf("service_ids: want empty, got %v", got.ServiceIDs)
	}
}

func TestUserScenarioUpdateMissingIsError(t *testing.T) {
	repo, ctx := userScenarioFixture(t)
	sc := sampleUserScenario()
	sc.ID = "00000000-0000-4009-8006-0000000000ff"

	if err := repo.UpdateUserScenario(ctx, sc); err == nil {
		t.Error("update of missing scenario: want error, got nil")
	}
}

func TestUserScenarioDelete(t *testing.T) {
	repo, ctx := userScenarioFixture(t)
	sc := sampleUserScenario()
	if err := repo.CreateUserScenario(ctx, sc); err != nil {
		t.Fatalf("CreateUserScenario: %v", err)
	}

	if err := repo.DeleteUserScenario(ctx, sc.ID); err != nil {
		t.Fatalf("DeleteUserScenario: %v", err)
	}
	if _, found, err := repo.GetUserScenarioByID(ctx, sc.ID); found || err != nil {
		t.Errorf("after delete: found=%v err=%v, want false/nil", found, err)
	}
}

func TestUserScenarioDeleteMissingIsError(t *testing.T) {
	repo, ctx := userScenarioFixture(t)
	if err := repo.DeleteUserScenario(ctx, "00000000-0000-4009-8006-0000000000ff"); err == nil {
		t.Error("delete of missing scenario: want error, got nil")
	}
}

func TestListUserScenariosByOwner(t *testing.T) {
	repo, ctx := userScenarioFixture(t)
	mine := sampleUserScenario()
	if err := repo.CreateUserScenario(ctx, mine); err != nil {
		t.Fatalf("CreateUserScenario mine: %v", err)
	}
	theirs := sampleUserScenario()
	theirs.ID = "00000000-0000-4009-8006-000000000003"
	theirs.Slug = "theirs"
	theirs.OwnerID = usnStrangerID
	theirs.ServiceIDs = nil
	if err := repo.CreateUserScenario(ctx, theirs); err != nil {
		t.Fatalf("CreateUserScenario theirs: %v", err)
	}

	got, err := repo.ListUserScenariosByOwner(ctx, usnOwnerID)
	if err != nil {
		t.Fatalf("ListUserScenariosByOwner: %v", err)
	}
	if len(got) != 1 || got[0].ID != mine.ID {
		t.Fatalf("got %+v, want just %q", got, mine.ID)
	}
	if len(got[0].ServiceIDs) != 2 {
		t.Errorf("hydrated service_ids: want 2, got %d", len(got[0].ServiceIDs))
	}
}

func TestUserServiceIDsOwnedBy(t *testing.T) {
	repo, ctx := userScenarioFixture(t)

	owned, err := repo.UserServiceIDsOwnedBy(ctx, usnOwnerID, []string{usnService1ID, usnService2ID, "no-such-id"})
	if err != nil {
		t.Fatalf("UserServiceIDsOwnedBy: %v", err)
	}
	if !owned[usnService1ID] || !owned[usnService2ID] {
		t.Errorf("owned services not reported as owned: %v", owned)
	}
	if owned["no-such-id"] {
		t.Errorf("unknown id reported as owned: %v", owned)
	}

	notMine, err := repo.UserServiceIDsOwnedBy(ctx, usnStrangerID, []string{usnService1ID})
	if err != nil {
		t.Fatalf("UserServiceIDsOwnedBy stranger: %v", err)
	}
	if notMine[usnService1ID] {
		t.Errorf("stranger reported as owning another user's service: %v", notMine)
	}
}
