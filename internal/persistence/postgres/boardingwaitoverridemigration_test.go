package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// rewindBoardingWaitOverrideMigration unwinds 00020 and is the current tail of
// the rewind chain that starts in snapmigration_test.go. Goose refuses to
// re-apply an earlier migration while a later version is still recorded, so
// anything rewinding a migration below this one must unrecord 00020 first —
// rewindPrerenderedIsochronesMigration does that by calling this.
//
// 00020 adds nullable columns, so unwinding it drops them as well as
// unrecording the version. A bare DELETE from goose_db_version would leave the
// columns behind; ADD COLUMN IF NOT EXISTS would then succeed as a no-op, which
// hides a rewind that did not actually rewind.
func rewindBoardingWaitOverrideMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`ALTER TABLE user_services DROP COLUMN IF EXISTS boarding_wait_policy, DROP COLUMN IF EXISTS boarding_wait_fixed_secs`,
		`ALTER TABLE user_scenarios DROP COLUMN IF EXISTS boarding_wait_policy, DROP COLUMN IF EXISTS boarding_wait_fixed_secs`,
		`ALTER TABLE services DROP COLUMN IF EXISTS boarding_wait_policy, DROP COLUMN IF EXISTS boarding_wait_fixed_secs`,
		`DELETE FROM goose_db_version WHERE version_id = 20`)
}

func TestBoardingWaitOverridesMigrationIsSafeToReRun(t *testing.T) {
	repo, _, url := userServiceFixture(t)

	// A user service row that existed before 00020: after migrate it must have
	// NULL override columns (inherit), matching SPA-237's "existing rows
	// migrate to null/inherit".
	svc := sampleUserService()
	if err := repo.CreateUserService(context.Background(), svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}

	rewindBoardingWaitOverrideMigration(t, url)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("first migration: %v", err)
	}

	got, found, err := repo.GetUserServiceByID(context.Background(), svc.ID)
	if err != nil || !found {
		t.Fatalf("GetUserServiceByID: found=%v err=%v", found, err)
	}
	if got.BoardingWait != nil {
		t.Errorf("BoardingWait after migrate = %+v, want nil (inherit)", got.BoardingWait)
	}

	// Forget that it ran while keeping the columns it added, so the second
	// pass meets a database that already has them. ADD COLUMN IF NOT EXISTS
	// must succeed rather than fail on "column already exists".
	exec(t, url, `DELETE FROM goose_db_version WHERE version_id = 20`)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migration re-run over columns it already added: %v", err)
	}

	got, found, err = repo.GetUserServiceByID(context.Background(), svc.ID)
	if err != nil || !found {
		t.Fatalf("GetUserServiceByID after re-run: found=%v err=%v", found, err)
	}
	if got.BoardingWait != nil {
		t.Errorf("BoardingWait after re-run = %+v, want nil", got.BoardingWait)
	}
}

func TestUserServiceBoardingWaitOverrideRoundTrip(t *testing.T) {
	repo, _, _ := userServiceFixture(t)
	svc := sampleUserService()
	secs := 120
	svc.BoardingWait = &transit.BoardingWaitOverride{
		Policy: transit.BoardingWaitFixed,
		Secs:   &secs,
	}
	if err := repo.CreateUserService(context.Background(), svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}

	got, found, err := repo.GetUserServiceByID(context.Background(), svc.ID)
	if err != nil || !found {
		t.Fatalf("GetUserServiceByID: found=%v err=%v", found, err)
	}
	if got.BoardingWait == nil {
		t.Fatal("BoardingWait = nil, want stored override")
	}
	if got.BoardingWait.Policy != transit.BoardingWaitFixed {
		t.Errorf("Policy = %q, want %q", got.BoardingWait.Policy, transit.BoardingWaitFixed)
	}
	if got.BoardingWait.Secs == nil || *got.BoardingWait.Secs != 120 {
		t.Errorf("Secs = %v, want 120", got.BoardingWait.Secs)
	}

	got.BoardingWait = nil
	if err := repo.UpdateUserService(context.Background(), got); err != nil {
		t.Fatalf("UpdateUserService clear: %v", err)
	}
	cleared, found, err := repo.GetUserServiceByID(context.Background(), svc.ID)
	if err != nil || !found {
		t.Fatalf("GetUserServiceByID after clear: found=%v err=%v", found, err)
	}
	if cleared.BoardingWait != nil {
		t.Errorf("BoardingWait after clear = %+v, want nil", cleared.BoardingWait)
	}
}

func TestUserScenarioBoardingWaitOverrideRoundTrip(t *testing.T) {
	repo, ctx := userScenarioFixture(t)
	sc := sampleUserScenario()
	sc.BoardingWait = &transit.BoardingWaitOverride{Policy: transit.BoardingWaitHalfHeadway}
	if err := repo.CreateUserScenario(ctx, sc); err != nil {
		t.Fatalf("CreateUserScenario: %v", err)
	}

	got, found, err := repo.GetUserScenarioByID(ctx, sc.ID)
	if err != nil || !found {
		t.Fatalf("GetUserScenarioByID: found=%v err=%v", found, err)
	}
	if got.BoardingWait == nil || got.BoardingWait.Policy != transit.BoardingWaitHalfHeadway {
		t.Errorf("BoardingWait = %+v, want half_headway", got.BoardingWait)
	}
}
