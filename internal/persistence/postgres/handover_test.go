package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

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
