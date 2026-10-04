package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestFailInterruptedJobsFailsOnlyUnfinishedJobsFromBeforeTheCutoff(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)
	conn := retentionConn(t, ctx, url)

	const (
		orphanRunning = "00000000-0000-400a-8431-000000000001"
		orphanQueued  = "00000000-0000-400a-8431-000000000002"
		oldSucceeded  = "00000000-0000-400a-8431-000000000003"
		oldFailed     = "00000000-0000-400a-8431-000000000004"
		freshRunning  = "00000000-0000-400a-8431-000000000005"
	)
	for _, j := range []struct{ id, status, errMsg string }{
		{orphanRunning, transit.JobStatusRunning, ""},
		{orphanQueued, transit.JobStatusQueued, ""},
		{oldSucceeded, transit.JobStatusSucceeded, ""},
		{oldFailed, transit.JobStatusFailed, "boom"},
		{freshRunning, transit.JobStatusRunning, ""},
	} {
		if err := repo.CreateJob(ctx, transit.Job{
			ID: j.id, Kind: transit.JobKindCompileScenario, Status: j.status, Error: j.errMsg,
		}); err != nil {
			t.Fatalf("CreateJob %s: %v", j.id, err)
		}
	}
	// Everything but freshRunning was enqueued by a process that has since died.
	retentionExec(t, ctx, conn,
		`UPDATE jobs SET created_at = now() - interval '1 hour' WHERE id <> $1`, freshRunning)

	n, err := repo.FailInterruptedJobs(ctx, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("FailInterruptedJobs: %v", err)
	}
	if n != 2 {
		t.Errorf("FailInterruptedJobs failed %d jobs, want 2", n)
	}

	want := map[string]struct{ status, errMsg string }{
		orphanRunning: {transit.JobStatusFailed, "interrupted by restart"},
		orphanQueued:  {transit.JobStatusFailed, "interrupted by restart"},
		oldSucceeded:  {transit.JobStatusSucceeded, ""},
		oldFailed:     {transit.JobStatusFailed, "boom"},
		freshRunning:  {transit.JobStatusRunning, ""},
	}
	for id, w := range want {
		got, ok, err := repo.GetJobByID(ctx, id)
		if err != nil || !ok {
			t.Fatalf("GetJobByID %s: ok=%v err=%v", id, ok, err)
		}
		if got.Status != w.status || got.Error != w.errMsg {
			t.Errorf("job %s = %s/%q, want %s/%q", id, got.Status, got.Error, w.status, w.errMsg)
		}
	}
}
