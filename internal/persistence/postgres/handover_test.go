package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const (
	hoID1 = "00000000-0000-400b-8003-000000000001"
	hoID2 = "00000000-0000-400b-8003-000000000002"
	hoID3 = "00000000-0000-400b-8003-000000000003"
)

func handoverFixture(t *testing.T) (*postgres.Repo, context.Context, string) {
	t.Helper()
	repo, ctx, url := userServiceFixture(t)
	if err := repo.CreateUserService(ctx, sampleUserService()); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}
	return repo, ctx, url
}

func offer(t *testing.T, repo *postgres.Repo, ctx context.Context, id string, ttl time.Duration) transit.ServiceHandover {
	t.Helper()
	h, err := repo.OfferServiceHandover(ctx, transit.ServiceHandover{
		ID: id, UserServiceID: usServiceID, FromUserID: usOwnerID, ToUserID: usStrangerID,
	}, ttl)
	if err != nil {
		t.Fatalf("OfferServiceHandover %s: %v", id, err)
	}
	return h
}

func TestOfferServiceHandoverIsListedForBothParties(t *testing.T) {
	repo, ctx, _ := handoverFixture(t)

	before := time.Now()
	got := offer(t, repo, ctx, hoID1, 14*24*time.Hour)
	if got.Status != transit.HandoverPending || got.ServiceSlug != "bay-area-express" ||
		got.ServiceName != "Bay Area Express" || got.FromName != "Owner" || got.ToName != "Stranger" {
		t.Fatalf("offered = %+v", got)
	}
	if got.DecidedAt != nil {
		t.Fatalf("decided_at = %v, want nil while pending", got.DecidedAt)
	}
	if d := got.ExpiresAt.Sub(before); d < 14*24*time.Hour-time.Minute || d > 14*24*time.Hour+time.Minute {
		t.Fatalf("expires_at %v is %v after the offer, want ~14 days", got.ExpiresAt, d)
	}

	for _, user := range []string{usOwnerID, usStrangerID} {
		list, err := repo.ListPendingServiceHandovers(ctx, user)
		if err != nil {
			t.Fatalf("ListPendingServiceHandovers %s: %v", user, err)
		}
		if len(list) != 1 || list[0].ID != hoID1 || list[0].ToName != "Stranger" || list[0].FromName != "Owner" {
			t.Fatalf("list for %s = %+v, want the one offer", user, list)
		}
	}
	pending, err := repo.HasPendingServiceHandover(ctx, usServiceID)
	if err != nil || !pending {
		t.Fatalf("HasPendingServiceHandover = %v, %v; want true", pending, err)
	}
}

func TestOfferServiceHandoverAllowsOnePendingPerService(t *testing.T) {
	repo, ctx, _ := handoverFixture(t)
	offer(t, repo, ctx, hoID1, time.Hour)

	_, err := repo.OfferServiceHandover(ctx, transit.ServiceHandover{
		ID: hoID2, UserServiceID: usServiceID, FromUserID: usOwnerID, ToUserID: usStrangerID,
	}, time.Hour)
	if !errors.Is(err, handler.ErrHandoverPending) {
		t.Fatalf("second pending offer: err = %v, want ErrHandoverPending", err)
	}

	// Once the first is closed, the service may be offered again.
	if _, err := repo.CancelServiceHandover(ctx, hoID1, usOwnerID); err != nil {
		t.Fatalf("CancelServiceHandover: %v", err)
	}
	offer(t, repo, ctx, hoID2, time.Hour)
}

func TestAnExpiredHandoverReadsAsExpiredAndFreesTheService(t *testing.T) {
	repo, ctx, _ := handoverFixture(t)
	offer(t, repo, ctx, hoID1, -time.Second)

	pending, err := repo.HasPendingServiceHandover(ctx, usServiceID)
	if err != nil || pending {
		t.Fatalf("HasPendingServiceHandover = %v, %v; want false for an expired offer", pending, err)
	}
	list, err := repo.ListPendingServiceHandovers(ctx, usStrangerID)
	if err != nil || len(list) != 0 {
		t.Fatalf("list = %+v, %v; want no expired offer", list, err)
	}
	if _, err := repo.CancelServiceHandover(ctx, hoID1, usOwnerID); !errors.Is(err, handler.ErrHandoverNotPending) {
		t.Fatalf("cancel expired: err = %v, want ErrHandoverNotPending", err)
	}
	if _, err := repo.DeclineServiceHandover(ctx, hoID1, usStrangerID); !errors.Is(err, handler.ErrHandoverNotPending) {
		t.Fatalf("decline expired: err = %v, want ErrHandoverNotPending", err)
	}

	// The expired row is still 'pending' on disk until something writes it;
	// a new offer must not trip the one-pending index over it.
	offer(t, repo, ctx, hoID2, time.Hour)
}

func TestCancelServiceHandoverBelongsToTheSender(t *testing.T) {
	repo, ctx, _ := handoverFixture(t)
	offer(t, repo, ctx, hoID1, time.Hour)

	if _, err := repo.CancelServiceHandover(ctx, hoID1, usStrangerID); !errors.Is(err, handler.ErrHandoverNotFound) {
		t.Fatalf("recipient cancel: err = %v, want ErrHandoverNotFound", err)
	}
	if _, err := repo.CancelServiceHandover(ctx, hoID3, usOwnerID); !errors.Is(err, handler.ErrHandoverNotFound) {
		t.Fatalf("unknown id: err = %v, want ErrHandoverNotFound", err)
	}

	got, err := repo.CancelServiceHandover(ctx, hoID1, usOwnerID)
	if err != nil {
		t.Fatalf("CancelServiceHandover: %v", err)
	}
	if got.Status != transit.HandoverCancelled || got.DecidedAt == nil || got.ToName != "Stranger" {
		t.Fatalf("cancelled = %+v", got)
	}
	if _, err := repo.CancelServiceHandover(ctx, hoID1, usOwnerID); !errors.Is(err, handler.ErrHandoverNotPending) {
		t.Fatalf("second cancel: err = %v, want ErrHandoverNotPending", err)
	}
	if _, err := repo.DeclineServiceHandover(ctx, hoID1, usStrangerID); !errors.Is(err, handler.ErrHandoverNotPending) {
		t.Fatalf("decline after cancel: err = %v, want ErrHandoverNotPending", err)
	}
	if list, _ := repo.ListPendingServiceHandovers(ctx, usOwnerID); len(list) != 0 {
		t.Fatalf("list after cancel = %+v, want empty", list)
	}
}

func TestDeclineServiceHandoverBelongsToTheRecipient(t *testing.T) {
	repo, ctx, _ := handoverFixture(t)
	offer(t, repo, ctx, hoID1, time.Hour)

	if _, err := repo.DeclineServiceHandover(ctx, hoID1, usOwnerID); !errors.Is(err, handler.ErrHandoverNotFound) {
		t.Fatalf("sender decline: err = %v, want ErrHandoverNotFound", err)
	}
	got, err := repo.DeclineServiceHandover(ctx, hoID1, usStrangerID)
	if err != nil {
		t.Fatalf("DeclineServiceHandover: %v", err)
	}
	if got.Status != transit.HandoverDeclined || got.DecidedAt == nil || got.FromName != "Owner" {
		t.Fatalf("declined = %+v", got)
	}
}

func TestDeletingTheServiceDeletesItsHandovers(t *testing.T) {
	repo, ctx, url := handoverFixture(t)
	offer(t, repo, ctx, hoID1, time.Hour)

	if err := repo.DeleteUserService(ctx, usServiceID); err != nil {
		t.Fatalf("DeleteUserService: %v", err)
	}
	if got := scalarCount(t, url, `SELECT count(*) FROM service_handovers`); got != 0 {
		t.Fatalf("handovers after delete = %d, want 0", got)
	}
}

func TestServiceHandoverMigrationIsSafeToReRun(t *testing.T) {
	_, ctx, url := handoverFixture(t)

	rewindTo(t, url, 32)
	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatalf("re-running 00032 over the table it already created: %v", err)
	}
}

const (
	hoThirdUserID = "00000000-0000-4007-8003-000000000003"
	hoOwnedRoute  = "00000000-0000-4002-8003-000000000009"
	hoJobID       = "00000000-0000-400a-8003-000000000001"
)

func fixtureService(t *testing.T, repo *postgres.Repo, ctx context.Context) transit.UserService {
	t.Helper()
	svc, found, err := repo.GetUserServiceByID(ctx, usServiceID)
	if err != nil || !found {
		t.Fatalf("GetUserServiceByID: found=%v err=%v", found, err)
	}
	return svc
}

func TestAcceptServiceHandoverMovesTheServiceAndItsJobsAndClosesTheOffer(t *testing.T) {
	repo, ctx, _ := handoverFixture(t)
	before := fixtureService(t, repo, ctx)
	owner := usOwnerID
	if err := repo.CreateJob(ctx, transit.Job{
		ID: hoJobID, Kind: transit.JobKindCompileUserService, Status: transit.JobStatusSucceeded,
		UserServiceID: &before.ID, OwnerID: &owner,
	}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	offer(t, repo, ctx, hoID1, time.Hour)

	got, err := repo.AcceptServiceHandover(ctx, hoID1, usStrangerID)
	if err != nil {
		t.Fatalf("AcceptServiceHandover: %v", err)
	}
	if got.Status != transit.HandoverAccepted || got.DecidedAt == nil || got.FromName != "Owner" || got.ToName != "Stranger" {
		t.Fatalf("accepted = %+v", got)
	}

	after := fixtureService(t, repo, ctx)
	if after.OwnerID != usStrangerID {
		t.Fatalf("owner_id = %s, want the recipient %s", after.OwnerID, usStrangerID)
	}
	if after.Slug != before.Slug || after.ID != before.ID {
		t.Fatalf("slug/id changed: %s/%s -> %s/%s", before.Slug, before.ID, after.Slug, after.ID)
	}
	// The fixture's route is curated (no owner), so the recipient can already
	// reference it and nothing is copied.
	if after.RouteID != before.RouteID {
		t.Fatalf("route_id = %s, want the curated route %s untouched", after.RouteID, before.RouteID)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("updated_at = %v, want later than %v", after.UpdatedAt, before.UpdatedAt)
	}
	job, found, err := repo.GetJobByID(ctx, hoJobID)
	if err != nil || !found {
		t.Fatalf("GetJobByID: found=%v err=%v", found, err)
	}
	if job.OwnerID == nil || *job.OwnerID != usStrangerID {
		t.Fatalf("job owner_id = %v, want the recipient", job.OwnerID)
	}

	for _, user := range []string{usOwnerID, usStrangerID} {
		if list, _ := repo.ListPendingServiceHandovers(ctx, user); len(list) != 0 {
			t.Fatalf("list for %s after accept = %+v, want empty", user, list)
		}
	}
	if _, err := repo.AcceptServiceHandover(ctx, hoID1, usStrangerID); !errors.Is(err, handler.ErrHandoverNotPending) {
		t.Fatalf("second accept: err = %v, want ErrHandoverNotPending", err)
	}
	if _, err := repo.CancelServiceHandover(ctx, hoID1, usOwnerID); !errors.Is(err, handler.ErrHandoverNotPending) {
		t.Fatalf("cancel after accept: err = %v, want ErrHandoverNotPending", err)
	}
}

func TestAcceptServiceHandoverBelongsToTheRecipient(t *testing.T) {
	repo, ctx, _ := handoverFixture(t)
	offer(t, repo, ctx, hoID1, time.Hour)

	if _, err := repo.AcceptServiceHandover(ctx, hoID1, usOwnerID); !errors.Is(err, handler.ErrHandoverNotFound) {
		t.Fatalf("sender accept: err = %v, want ErrHandoverNotFound", err)
	}
	if _, err := repo.AcceptServiceHandover(ctx, hoID3, usStrangerID); !errors.Is(err, handler.ErrHandoverNotFound) {
		t.Fatalf("unknown id: err = %v, want ErrHandoverNotFound", err)
	}
	if got := fixtureService(t, repo, ctx); got.OwnerID != usOwnerID {
		t.Fatalf("owner_id = %s after refused accepts, want unchanged", got.OwnerID)
	}
}

func TestAcceptServiceHandoverRefusesAnExpiredOffer(t *testing.T) {
	repo, ctx, _ := handoverFixture(t)
	offer(t, repo, ctx, hoID1, -time.Second)

	if _, err := repo.AcceptServiceHandover(ctx, hoID1, usStrangerID); !errors.Is(err, handler.ErrHandoverNotPending) {
		t.Fatalf("accept expired: err = %v, want ErrHandoverNotPending", err)
	}
	if got := fixtureService(t, repo, ctx); got.OwnerID != usOwnerID {
		t.Fatalf("owner_id = %s, want unchanged", got.OwnerID)
	}
}

func TestAcceptServiceHandoverRefusesAServiceStillInASenderScenario(t *testing.T) {
	repo, ctx, _ := handoverFixture(t)
	for i, slug := range []string{"weekend-trips", "commute"} {
		if err := repo.CreateUserScenario(ctx, transit.UserScenario{
			ID: fmt.Sprintf("00000000-0000-4009-8003-00000000000%d", i+1), Slug: slug, OwnerID: usOwnerID,
			Name: slug, ServiceIDs: []string{usServiceID},
		}); err != nil {
			t.Fatalf("CreateUserScenario %s: %v", slug, err)
		}
	}
	offer(t, repo, ctx, hoID1, time.Hour)

	_, err := repo.AcceptServiceHandover(ctx, hoID1, usStrangerID)
	var inScenarios *handler.ServiceInScenariosError
	if !errors.As(err, &inScenarios) {
		t.Fatalf("accept: err = %v, want *ServiceInScenariosError", err)
	}
	if len(inScenarios.Slugs) != 2 || inScenarios.Slugs[0] != "commute" || inScenarios.Slugs[1] != "weekend-trips" {
		t.Fatalf("slugs = %v, want [commute weekend-trips]", inScenarios.Slugs)
	}

	// Nothing moved and the offer is still open for the sender to act on.
	if got := fixtureService(t, repo, ctx); got.OwnerID != usOwnerID {
		t.Fatalf("owner_id = %s, want unchanged", got.OwnerID)
	}
	pending, err := repo.HasPendingServiceHandover(ctx, usServiceID)
	if err != nil || !pending {
		t.Fatalf("HasPendingServiceHandover = %v, %v; want still pending", pending, err)
	}
}

func TestAcceptServiceHandoverCopiesASenderOwnedRouteAndLeavesTheOriginal(t *testing.T) {
	repo, ctx, _ := handoverFixture(t)
	owner := usOwnerID
	original := transit.Route{
		ID: hoOwnedRoute, OwnerID: &owner, Slug: "my-alignment", Name: "My Alignment",
		Description: "Drawn by hand", Mode: "rail", Bidirectional: false,
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122.4, 37.7}, {-122.3, 37.6}, {-121.9, 37.3}}},
		Segments: []transit.RouteSegment{{CantMM: 100, CurveRadiusM: 2000, GradePct: 1.5}, {CantMM: 0, CurveRadiusM: 0, GradePct: -0.5}},
	}
	if err := repo.CreateRoute(ctx, original); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	svc := fixtureService(t, repo, ctx)
	svc.RouteID = hoOwnedRoute
	if err := repo.UpdateUserService(ctx, svc); err != nil {
		t.Fatalf("UpdateUserService: %v", err)
	}
	offer(t, repo, ctx, hoID1, time.Hour)

	if _, err := repo.AcceptServiceHandover(ctx, hoID1, usStrangerID); err != nil {
		t.Fatalf("AcceptServiceHandover: %v", err)
	}

	after := fixtureService(t, repo, ctx)
	if after.RouteID == hoOwnedRoute {
		t.Fatalf("route_id still %s, want a copy the recipient owns", hoOwnedRoute)
	}
	routes, err := repo.ListRoutesByIDs(ctx, []string{hoOwnedRoute, after.RouteID})
	if err != nil || len(routes) != 2 {
		t.Fatalf("ListRoutesByIDs: %d routes, err %v", len(routes), err)
	}
	var kept, copied transit.Route
	for _, rt := range routes {
		if rt.ID == hoOwnedRoute {
			kept = rt
		} else {
			copied = rt
		}
	}
	if kept.OwnerID == nil || *kept.OwnerID != usOwnerID || kept.Slug != "my-alignment" {
		t.Fatalf("sender's route changed: %+v", kept)
	}
	if copied.OwnerID == nil || *copied.OwnerID != usStrangerID {
		t.Fatalf("copy owner = %v, want the recipient", copied.OwnerID)
	}
	if copied.Slug == kept.Slug || copied.Slug == "" {
		t.Fatalf("copy slug = %q, want a fresh one", copied.Slug)
	}
	if copied.ScenarioID != nil {
		t.Fatalf("copy scenario_id = %v, want none: the sender's scenario is not the recipient's", *copied.ScenarioID)
	}
	if copied.Name != kept.Name || copied.Description != kept.Description || copied.Mode != kept.Mode ||
		copied.Bidirectional != kept.Bidirectional {
		t.Fatalf("copy %+v differs from original %+v beyond id, slug and owner", copied, kept)
	}
	if fmt.Sprint(copied.Geometry) != fmt.Sprint(kept.Geometry) || fmt.Sprint(copied.Segments) != fmt.Sprint(kept.Segments) {
		t.Fatalf("copy geometry/segments differ:\n got  %v %v\n want %v %v",
			copied.Geometry, copied.Segments, kept.Geometry, kept.Segments)
	}
	if after.RouteSlug != copied.Slug {
		t.Fatalf("service route_slug = %s, want the copy %s", after.RouteSlug, copied.Slug)
	}
}

func TestAcceptServiceHandoverRefusesWhenTheSenderNoLongerOwnsTheService(t *testing.T) {
	repo, ctx, _ := handoverFixture(t)
	if err := repo.CreateUser(ctx, account.User{ID: hoThirdUserID, Email: "third@example.com", Name: "Third"}, ""); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	offer(t, repo, ctx, hoID1, time.Hour)
	if err := repo.TransferUserService(ctx, usServiceID, hoThirdUserID); err != nil {
		t.Fatalf("TransferUserService: %v", err)
	}

	if _, err := repo.AcceptServiceHandover(ctx, hoID1, usStrangerID); !errors.Is(err, handler.ErrHandoverSenderNotOwner) {
		t.Fatalf("accept: err = %v, want ErrHandoverSenderNotOwner", err)
	}
	if got := fixtureService(t, repo, ctx); got.OwnerID != hoThirdUserID {
		t.Fatalf("owner_id = %s, want the third user", got.OwnerID)
	}
}

func TestAcceptServiceHandoverRefusesADisabledRecipient(t *testing.T) {
	repo, ctx, _ := handoverFixture(t)
	offer(t, repo, ctx, hoID1, time.Hour)
	if err := repo.SetUserDisabled(ctx, usStrangerID, true); err != nil {
		t.Fatalf("SetUserDisabled: %v", err)
	}

	if _, err := repo.AcceptServiceHandover(ctx, hoID1, usStrangerID); !errors.Is(err, handler.ErrHandoverRecipientDisabled) {
		t.Fatalf("accept: err = %v, want ErrHandoverRecipientDisabled", err)
	}
	if got := fixtureService(t, repo, ctx); got.OwnerID != usOwnerID {
		t.Fatalf("owner_id = %s, want unchanged", got.OwnerID)
	}
}

func TestTransferUserServiceRefusesAServiceInAScenario(t *testing.T) {
	repo, ctx, _ := handoverFixture(t)
	if err := repo.CreateUserScenario(ctx, transit.UserScenario{
		ID: "00000000-0000-4009-8003-000000000001", Slug: "commute", OwnerID: usOwnerID,
		Name: "Commute", ServiceIDs: []string{usServiceID},
	}); err != nil {
		t.Fatalf("CreateUserScenario: %v", err)
	}

	err := repo.TransferUserService(ctx, usServiceID, usStrangerID)
	var inScenarios *handler.ServiceInScenariosError
	if !errors.As(err, &inScenarios) || len(inScenarios.Slugs) != 1 || inScenarios.Slugs[0] != "commute" {
		t.Fatalf("TransferUserService: err = %v, want *ServiceInScenariosError{commute}", err)
	}
}
