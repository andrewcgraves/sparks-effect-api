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

	if transit.GraphStale(job, current, updatedAt, nil) {
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

	if !transit.GraphStale(job, current, updatedAt, nil) {
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

	if !transit.GraphStale(job, current, updatedAt, nil) {
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

	if !transit.GraphStale(job, current, updatedAt, nil) {
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

	if !transit.GraphStale(job, current, updatedAt, nil) {
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

	if !transit.GraphStale(job, current, updatedAt, nil) {
		t.Error("GraphStale = false, want true: edit fell between created_at and completion")
	}
}

func TestGraphStale_emptyScenario(t *testing.T) {
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	job := transit.Job{CreatedAt: created, CompiledServiceIDs: nil}

	if transit.GraphStale(job, nil, nil, nil) {
		t.Error("GraphStale = true, want false: no members compiled, none present now")
	}
}

func TestGraphStale_boardingWaitPolicyChange(t *testing.T) {
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	job := transit.Job{
		CreatedAt:          created,
		CompiledServiceIDs: []string{"svc-1"},
		Result: &transit.TransitGraph{Services: []transit.ServiceGraph{{
			ServiceID:  "svc-1",
			WaitSecs:   0,
			WaitPolicy: string(transit.BoardingWaitNone),
		}}},
	}
	current := []string{"svc-1"}
	updatedAt := map[string]time.Time{"svc-1": created.Add(-time.Hour)}

	if transit.GraphStale(job, current, updatedAt, map[string]transit.BoardingWaitPolicy{
		"svc-1": transit.DefaultBoardingWaitPolicy(),
	}) {
		t.Error("GraphStale = true, want false: policy unchanged")
	}
	if !transit.GraphStale(job, current, updatedAt, map[string]transit.BoardingWaitPolicy{
		"svc-1": {Kind: transit.BoardingWaitHalfHeadway},
	}) {
		t.Error("GraphStale = false, want true: boarding-wait policy changed")
	}
}

func TestGraphStale_perServiceOverrideChange(t *testing.T) {
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	job := transit.Job{
		CreatedAt:          created,
		CompiledServiceIDs: []string{"svc-1", "svc-2"},
		Result: &transit.TransitGraph{Services: []transit.ServiceGraph{
			{ServiceID: "svc-1", WaitSecs: 0, WaitPolicy: string(transit.BoardingWaitNone)},
			{ServiceID: "svc-2", WaitSecs: 0, WaitPolicy: string(transit.BoardingWaitNone)},
		}},
	}
	current := []string{"svc-1", "svc-2"}
	updatedAt := map[string]time.Time{
		"svc-1": created.Add(-time.Hour),
		"svc-2": created.Add(-time.Hour),
	}

	unchanged := map[string]transit.BoardingWaitPolicy{
		"svc-1": transit.DefaultBoardingWaitPolicy(),
		"svc-2": transit.DefaultBoardingWaitPolicy(),
	}
	if transit.GraphStale(job, current, updatedAt, unchanged) {
		t.Error("GraphStale = true, want false: neither override moved")
	}

	svc1Overridden := map[string]transit.BoardingWaitPolicy{
		"svc-1": {Kind: transit.BoardingWaitFixed, FixedSecs: 120},
		"svc-2": transit.DefaultBoardingWaitPolicy(),
	}
	if !transit.GraphStale(job, current, updatedAt, svc1Overridden) {
		t.Error("GraphStale = false, want true: one member's override changed")
	}
}

func TestGraphStale_scenarioOverrideChangeWithoutMemberEdit(t *testing.T) {
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	job := transit.Job{
		CreatedAt:          created,
		CompiledServiceIDs: []string{"svc-1"},
		Result: &transit.TransitGraph{Services: []transit.ServiceGraph{{
			ServiceID:  "svc-1",
			WaitSecs:   1800,
			WaitPolicy: string(transit.BoardingWaitHalfHeadway),
		}}},
	}
	current := []string{"svc-1"}
	// Member timestamps are still older than the compile — a scenario-only
	// override change does not bump them.
	updatedAt := map[string]time.Time{"svc-1": created.Add(-time.Hour)}

	if transit.GraphStale(job, current, updatedAt, map[string]transit.BoardingWaitPolicy{
		"svc-1": {Kind: transit.BoardingWaitHalfHeadway},
	}) {
		t.Error("GraphStale = true, want false: scenario override still half_headway")
	}
	if !transit.GraphStale(job, current, updatedAt, map[string]transit.BoardingWaitPolicy{
		"svc-1": transit.DefaultBoardingWaitPolicy(),
	}) {
		t.Error("GraphStale = false, want true: scenario override cleared back to global none")
	}
}

func TestGraphStale_preSPA236GraphMissingWaitPolicyIsStale(t *testing.T) {
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	job := transit.Job{
		CreatedAt:          created,
		CompiledServiceIDs: []string{"svc-1"},
		Result: &transit.TransitGraph{Services: []transit.ServiceGraph{{
			ServiceID: "svc-1",
			WaitSecs:  1800, // historical half_headway bake-in, no WaitPolicy field
		}}},
	}
	current := []string{"svc-1"}
	updatedAt := map[string]time.Time{"svc-1": created.Add(-time.Hour)}

	if !transit.GraphStale(job, current, updatedAt, nil) {
		t.Error("GraphStale = false, want true: empty WaitPolicy must not silently match none")
	}
}

// SPA-264 gives every compiled edge the route it runs over. A graph compiled
// before that carries none, which is the same shape an authored graph can never
// legitimately have — the physics compiler always knows its service's one
// alignment — so an edge with no route means the graph predates the feature.
//
// This is the sibling of TestGraphStale_preSPA236GraphMissingWaitPolicyIsStale:
// both make an invariant a fresh graph carries actually true, rather than hoped
// for, by declaring an old graph out of date. The author pays one recompile,
// which the authored-graph composable already performs on its own when a stale
// graph is refused.
func TestGraphStale_preSPA264GraphMissingEdgeRoutesIsStale(t *testing.T) {
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	current := []string{"svc-1"}
	updatedAt := map[string]time.Time{"svc-1": created.Add(-time.Hour)}
	policies := map[string]transit.BoardingWaitPolicy{"svc-1": transit.DefaultBoardingWaitPolicy()}

	graphWith := func(edges ...transit.Edge) transit.Job {
		return transit.Job{
			CreatedAt:          created,
			CompiledServiceIDs: []string{"svc-1"},
			Result: &transit.TransitGraph{Services: []transit.ServiceGraph{{
				ServiceID:  "svc-1",
				WaitPolicy: string(transit.BoardingWaitNone),
				Edges:      edges,
			}}},
		}
	}

	stale := graphWith(transit.Edge{FromSlug: "a", ToSlug: "b", Seconds: 600})
	if !transit.GraphStale(stale, current, updatedAt, policies) {
		t.Error("GraphStale = false, want true: an edge with no route id predates SPA-264")
	}

	fresh := graphWith(transit.Edge{
		FromSlug: "a", ToSlug: "b", Seconds: 600,
		RouteID: "rt-1", FromChainageM: 0, ToChainageM: 1200,
	})
	if transit.GraphStale(fresh, current, updatedAt, policies) {
		t.Error("GraphStale = true, want false: every edge names the route it runs over")
	}

	// A service with no edges at all — a single-stop service — has nothing to
	// carry a route, and must not be read as out of date forever.
	if transit.GraphStale(graphWith(), current, updatedAt, policies) {
		t.Error("GraphStale = true, want false: a service with no edges cannot be missing edge routes")
	}
}
