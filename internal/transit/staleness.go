package transit

import "time"

// GraphStale reports whether job's compiled graph no longer reflects a user
// scenario's current state (SPA-83 decision 4, corrected by SPA-116; boarding
// wait policy added by SPA-236).
//
// The original rule compared timestamps alone —
// max(scenario.updated_at, max(member.updated_at)) > job.created_at — which
// cannot detect a deleted member: user_scenario_services.user_service_id is
// ON DELETE CASCADE, so a delete removes the join row without ever touching
// user_scenarios.updated_at, and the max of a smaller set can only be less
// than or equal to the max of the larger one. Deletion is not merely missed;
// the expression is monotone in the wrong direction.
//
// This rule instead compares currentServiceIDs, the scenario's live
// membership, against job.CompiledServiceIDs, the snapshot taken when the
// graph was built. Any difference — an addition, a removal, or a deletion —
// makes the set comparison fail and the graph is stale, uniformly and without
// depending on any timestamp. Once membership matches, staleness falls back to
// the one question timestamps are actually good at: whether a still-present
// member changed since compile, via currentServiceUpdatedAt.
//
// currentPolicy is the boarding-wait policy the next compile would bake in.
// A global policy change touches neither membership nor updated_at, so without
// this check a user-authored graph would silently keep charging the old wait.
// Each ServiceGraph records the policy that produced its WaitSecs; any mismatch
// (including a pre-SPA-236 graph with an empty WaitPolicy) is stale. For fixed
// policies the seconds are compared too, so changing BOARDING_WAIT_FIXED_SECS
// alone is caught.
//
// The comparison point is job.CreatedAt, not a completion timestamp, and must
// stay that way: job created T0, worker reads data at T0+2, user edits at
// T0+3, job completes at T0+4 carrying pre-edit data. Against created_at
// (T0), T0+3 > T0 is stale — correct, the graph really is out of date.
// Against a completion time (T0+4), T0+3 < T0+4 is fresh — wrong, and
// silently so. Comparing against created_at can produce one spurious 409 and
// recompile in the narrow window before the worker's read, which converges
// harmlessly; comparing against completion cannot detect the edit at all.
func GraphStale(job Job, currentServiceIDs []string, currentServiceUpdatedAt map[string]time.Time, currentPolicy BoardingWaitPolicy) bool {
	compiled := make(map[string]bool, len(job.CompiledServiceIDs))
	for _, id := range job.CompiledServiceIDs {
		compiled[id] = true
	}
	if len(compiled) != len(currentServiceIDs) {
		return true
	}
	for _, id := range currentServiceIDs {
		if !compiled[id] {
			return true
		}
	}
	for _, id := range currentServiceIDs {
		if t, ok := currentServiceUpdatedAt[id]; ok && t.After(job.CreatedAt) {
			return true
		}
	}
	if job.Result != nil && boardingWaitStale(*job.Result, currentPolicy) {
		return true
	}
	return false
}

// boardingWaitStale reports whether any service graph was compiled under a
// different boarding-wait policy (or fixed seconds) than currentPolicy.
func boardingWaitStale(graph TransitGraph, current BoardingWaitPolicy) bool {
	wantKind := current.kindOrNone()
	for _, sg := range graph.Services {
		// WaitPolicy is a string on the wire (jobs.result JSON); compare it as
		// the kind it represents so an unknown value cannot pass for a valid one.
		if BoardingWaitKind(sg.WaitPolicy) != wantKind {
			return true
		}
		if current.Kind == BoardingWaitFixed && sg.WaitSecs != current.FixedSecs {
			return true
		}
	}
	return false
}
