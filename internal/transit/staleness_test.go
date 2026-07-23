package transit_test

import (
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestGraphStale_freshWhenMembershipAndTimestampsUnchanged(t *testing.T) {
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	job := transit.Job{CreatedAt: created, CompiledServiceIDs: []string{"svc-1", "svc-2"}}
	current := []string{"svc-1", "svc-2"}
	updatedAt := map[string]time.Time{
		"svc-1": created.Add(-time.Hour),
		"svc-2": created.Add(-time.Minute),
	}

	if transit.GraphStale(job, current, updatedAt) {
		t.Error("GraphStale = true, want false: membership and timestamps unchanged")
	}
}

func TestGraphStale_deletedMember(t *testing.T) {
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	job := transit.Job{CreatedAt: created, CompiledServiceIDs: []string{"svc-1", "svc-2"}}
	// svc-2 was deleted: it cascades out of membership without touching any
	// timestamp the old rule compared, which is exactly the gap SPA-116 fixes.
	current := []string{"svc-1"}
	updatedAt := map[string]time.Time{
		"svc-1": created.Add(-time.Hour),
	}

	if !transit.GraphStale(job, current, updatedAt) {
		t.Error("GraphStale = false, want true: a compiled member is no longer in the current set")
	}
}

func TestGraphStale_removedMemberWithoutDeletion(t *testing.T) {
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	job := transit.Job{CreatedAt: created, CompiledServiceIDs: []string{"svc-1", "svc-2"}}
	current := []string{"svc-1"}
	updatedAt := map[string]time.Time{
		"svc-1": created.Add(-time.Hour),
	}

	if !transit.GraphStale(job, current, updatedAt) {
		t.Error("GraphStale = false, want true: membership shrank")
	}
}

func TestGraphStale_addedMember(t *testing.T) {
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	job := transit.Job{CreatedAt: created, CompiledServiceIDs: []string{"svc-1"}}
	current := []string{"svc-1", "svc-2"}
	updatedAt := map[string]time.Time{
		"svc-1": created.Add(-time.Hour),
		"svc-2": created.Add(-time.Minute),
	}

	if !transit.GraphStale(job, current, updatedAt) {
		t.Error("GraphStale = false, want true: membership grew")
	}
}

func TestGraphStale_stillPresentMemberEditedAfterCompile(t *testing.T) {
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	job := transit.Job{CreatedAt: created, CompiledServiceIDs: []string{"svc-1", "svc-2"}}
	current := []string{"svc-1", "svc-2"}
	updatedAt := map[string]time.Time{
		"svc-1": created.Add(-time.Hour),
		"svc-2": created.Add(time.Minute), // edited after the job was created
	}

	if !transit.GraphStale(job, current, updatedAt) {
		t.Error("GraphStale = false, want true: a still-present member changed after compile")
	}
}

// A job's own creation time, not a later completion time, is the correct
// comparison point (see the reasoning on GraphStale). This test pins the
// asymmetry so it isn't "corrected" into comparing UpdatedAt instead.
func TestGraphStale_comparesAgainstCreatedAtNotUpdatedAt(t *testing.T) {
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	job := transit.Job{
		CreatedAt:          created,
		UpdatedAt:          created.Add(time.Hour), // completion time, well after the edit below
		CompiledServiceIDs: []string{"svc-1"},
	}
	current := []string{"svc-1"}
	updatedAt := map[string]time.Time{
		// Edited between created_at and updated_at (completion) — must still
		// register as stale, or a completion-time comparison bug has crept back in.
		"svc-1": created.Add(time.Minute),
	}

	if !transit.GraphStale(job, current, updatedAt) {
		t.Error("GraphStale = false, want true: edit fell between created_at and completion")
	}
}

func TestGraphStale_emptyScenario(t *testing.T) {
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	job := transit.Job{CreatedAt: created, CompiledServiceIDs: nil}

	if transit.GraphStale(job, nil, nil) {
		t.Error("GraphStale = true, want false: no members compiled, none present now")
	}
}
