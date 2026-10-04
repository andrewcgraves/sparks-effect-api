package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestFailInterruptedJobLeavesAFinishedJobAlone(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	const (
		running  = "00000000-0000-400a-8431-000000000011"
		finished = "00000000-0000-400a-8431-000000000012"
	)
	for id, status := range map[string]string{running: transit.JobStatusRunning, finished: transit.JobStatusSucceeded} {
		if err := repo.CreateJob(ctx, transit.Job{ID: id, Kind: transit.JobKindCompileScenario, Status: status}); err != nil {
			t.Fatalf("CreateJob %s: %v", id, err)
		}
	}

	for _, tc := range []struct {
		id         string
		wantFailed bool
		wantStatus string
	}{
		{running, true, transit.JobStatusFailed},
		{finished, false, transit.JobStatusSucceeded},
	} {
		failed, err := repo.FailInterruptedJob(ctx, tc.id, "gone")
		if err != nil {
			t.Fatalf("FailInterruptedJob %s: %v", tc.id, err)
		}
		if failed != tc.wantFailed {
			t.Errorf("FailInterruptedJob %s = %v, want %v", tc.id, failed, tc.wantFailed)
		}
		got, _, _ := repo.GetJobByID(ctx, tc.id)
		if got.Status != tc.wantStatus {
			t.Errorf("job %s status = %s, want %s", tc.id, got.Status, tc.wantStatus)
		}
	}
}

func TestFailInterruptedJobsFailsEveryUnfinishedJobAndNoOther(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	const (
		orphanRunning = "00000000-0000-400a-8431-000000000001"
		orphanQueued  = "00000000-0000-400a-8431-000000000002"
		succeeded     = "00000000-0000-400a-8431-000000000003"
		failed        = "00000000-0000-400a-8431-000000000004"
	)
	for _, j := range []struct{ id, status, errMsg string }{
		{orphanRunning, transit.JobStatusRunning, ""},
		{orphanQueued, transit.JobStatusQueued, ""},
		{succeeded, transit.JobStatusSucceeded, ""},
		{failed, transit.JobStatusFailed, "boom"},
	} {
		if err := repo.CreateJob(ctx, transit.Job{
			ID: j.id, Kind: transit.JobKindCompileScenario, Status: j.status, Error: j.errMsg,
		}); err != nil {
			t.Fatalf("CreateJob %s: %v", j.id, err)
		}
	}

	n, err := repo.FailInterruptedJobs(ctx)
	if err != nil {
		t.Fatalf("FailInterruptedJobs: %v", err)
	}
	if n != 2 {
		t.Errorf("FailInterruptedJobs failed %d jobs, want 2", n)
	}

	want := map[string]struct{ status, errMsg string }{
		orphanRunning: {transit.JobStatusFailed, "interrupted by restart"},
		orphanQueued:  {transit.JobStatusFailed, "interrupted by restart"},
		succeeded:     {transit.JobStatusSucceeded, ""},
		failed:        {transit.JobStatusFailed, "boom"},
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
