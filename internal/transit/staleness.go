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
// The membership half of that rule — the set comparison and the
// still-present-member timestamp fallback — lives in MembershipStale below,
// which a prerendered isochrone shares. What stays here is the part only a
// compiled graph has: the boarding-wait policy baked into it.
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
	if MembershipStale(job.CompiledServiceIDs, job.CreatedAt, currentServiceIDs, currentServiceUpdatedAt) {
		return true
	}
	if job.Result != nil && boardingWaitStale(*job.Result, currentPolicy) {
		return true
	}
	return false
}

// MembershipStale is the two-step comparison above, without the boarding-wait
// policy check that only a compiled graph can answer: does the snapshot of
// service ids taken at capturedAt still match the scenario's live membership,
// and if it does, has any still-present member changed since?
//
// It is exported and separate because a compiled graph is not the only thing
// that goes out of date this way. A prerendered isochrone (PrerenderedIsochrone)
// is likewise a payload computed from a set of services at a moment in time,
// and it goes stale for exactly the reasons documented on GraphStale — a member
// added, removed, deleted, or edited. Reimplementing the comparison there would
// be a second copy of a rule SPA-116 already got wrong once; sharing it means a
// correction to one is a correction to both.
//
// compiledServiceIDs is the membership snapshot, capturedAt the moment it was
// taken (the compile job's CreatedAt, or the prerendered entry's). What differs
// between callers is only what they do about the answer: a stale graph is
// refused with a 409, while a stale prerendered isochrone is still served and
// merely reported as outdated.
func MembershipStale(compiledServiceIDs []string, capturedAt time.Time,
	currentServiceIDs []string, currentServiceUpdatedAt map[string]time.Time) bool {
	compiled := make(map[string]bool, len(compiledServiceIDs))
	for _, id := range compiledServiceIDs {
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
		if t, ok := currentServiceUpdatedAt[id]; ok && t.After(capturedAt) {
			return true
		}
	}
	return false
}

// PrerenderedOutdated reports whether a curated isochrone's payload was
// computed against a service set the scenario no longer has.
//
// Unlike GraphStale's caller, nothing refuses or hides an outdated entry: it is
// reported as "outdated": true on the wire and served unchanged. A prerendered
// payload is an illustration whose provenance is worth stating, not a graph an
// isochrone is about to be plotted over.
func PrerenderedOutdated(p PrerenderedIsochrone, members []ServiceMembership) bool {
	ids := make([]string, 0, len(members))
	updatedAt := make(map[string]time.Time, len(members))
	for _, m := range members {
		ids = append(ids, m.ServiceID)
		updatedAt[m.ServiceID] = m.UpdatedAt
	}
	return MembershipStale(p.CompiledServiceIDs, p.CreatedAt, ids, updatedAt)
}

// MembershipIDs reduces a membership set to the service ids it names, the form
// a snapshot is stored in (PrerenderedIsochrone.CompiledServiceIDs).
func MembershipIDs(members []ServiceMembership) []string {
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.ServiceID)
	}
	return ids
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
