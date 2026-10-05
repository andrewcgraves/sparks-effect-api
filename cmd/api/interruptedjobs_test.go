package main

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/testdb"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/jackc/pgx/v5"
)

func TestBootFailsACompileJobOrphanedByThePreviousProcess(t *testing.T) {
	ctx := context.Background()
	url := testdb.Fresh(t)

	// What a redeploy mid-compile leaves behind: a running row, enqueued before
	// this boot, whose goroutine died with the old process.
	const orphan = "00000000-0000-400a-8431-000000000101"
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := conn.Exec(ctx,
		`INSERT INTO jobs (id, kind, status, created_at, updated_at)
		 VALUES ($1, $2, $3, now() - interval '1 hour', now() - interval '1 hour')`,
		orphan, transit.JobKindCompileUserService, transit.JobStatusRunning); err != nil {
		t.Fatalf("insert orphan: %v", err)
	}
	_ = conn.Close(ctx)

	_, repo, cleanup, err := loadStore(ctx, config.Config{
		DatabaseURL:  url,
		BoardingWait: transit.DefaultBoardingWaitPolicy(),
	}, logger.Discard())
	if err != nil {
		t.Fatalf("loadStore: %v", err)
	}
	t.Cleanup(cleanup)

	got, found, err := repo.GetJobByID(ctx, orphan)
	if err != nil || !found {
		t.Fatalf("GetJobByID: found=%v err=%v", found, err)
	}
	if got.Status != transit.JobStatusFailed || got.Error != "interrupted by restart" {
		t.Errorf("orphaned job after boot = %s/%q, want failed/%q", got.Status, got.Error, "interrupted by restart")
	}

	// The seeded compiles boot itself ran are this process's own, and the sweep
	// must not have touched them.
	jobs, err := repo.ListJobs(ctx)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	for _, j := range jobs {
		if j.ID != orphan && j.Status != transit.JobStatusSucceeded {
			t.Errorf("boot's own compile %s = %s/%q, want succeeded", j.ID, j.Status, j.Error)
		}
	}
}
