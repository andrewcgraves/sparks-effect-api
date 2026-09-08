package transit

import "time"

func GraphStale(job Job, currentServiceIDs []string, currentServiceUpdatedAt map[string]time.Time, currentPolicies map[string]BoardingWaitPolicy) bool {
	if MembershipStale(job.CompiledServiceIDs, job.CreatedAt, currentServiceIDs, currentServiceUpdatedAt) {
		return true
	}
	if job.Result != nil && boardingWaitStale(*job.Result, currentPolicies) {
		return true
	}
	if job.Result != nil && edgeRoutesStale(*job.Result) {
		return true
	}
	return false
}

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

func PrerenderedOutdated(p PrerenderedIsochrone, members []ServiceMembership) bool {
	ids := make([]string, 0, len(members))
	updatedAt := make(map[string]time.Time, len(members))
	for _, m := range members {
		ids = append(ids, m.ServiceID)
		updatedAt[m.ServiceID] = m.UpdatedAt
	}
	return MembershipStale(p.CompiledServiceIDs, p.CreatedAt, ids, updatedAt)
}

func MembershipIDs(members []ServiceMembership) []string {
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.ServiceID)
	}
	return ids
}

func boardingWaitStale(graph TransitGraph, currentPolicies map[string]BoardingWaitPolicy) bool {
	for _, sg := range graph.Services {
		current, ok := currentPolicies[sg.ServiceID]
		if !ok {
			current = DefaultBoardingWaitPolicy()
		}
		wantKind := current.kindOrNone()
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

func edgeRoutesStale(graph TransitGraph) bool {
	for _, sg := range graph.Services {
		for _, e := range sg.Edges {
			if e.RouteID == "" {
				return true
			}
		}
	}
	return false
}
