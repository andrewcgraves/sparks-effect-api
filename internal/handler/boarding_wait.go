package handler

import "github.com/andrewcgraves/sparks-effect-api/internal/transit"

// withBoardingWait fills the response-only boarding-wait fields on a seeded
// service from the global policy and the service's frequency windows.
func withBoardingWait(svc transit.Service, policy transit.BoardingWaitPolicy) transit.Service {
	kind, secs, err := policy.ResolveForWindows(svc.FrequencyWindows)
	if err != nil {
		// Policy was validated at config load; a resolution fault here would be
		// a programming error. Leave the fields zero rather than invent a wait.
		return svc
	}
	svc.BoardingWaitPolicy = kind
	svc.BoardingWaitSecs = secs
	return svc
}

func withUserBoardingWait(svc transit.UserService, policy transit.BoardingWaitPolicy) transit.UserService {
	kind, secs, err := policy.ResolveForWindows(svc.FrequencyWindows)
	if err != nil {
		return svc
	}
	svc.BoardingWaitPolicy = kind
	svc.BoardingWaitSecs = secs
	return svc
}

func withBoardingWaits(svcs []transit.Service, policy transit.BoardingWaitPolicy) []transit.Service {
	out := make([]transit.Service, len(svcs))
	for i, svc := range svcs {
		out[i] = withBoardingWait(svc, policy)
	}
	return out
}

func withUserBoardingWaits(svcs []transit.UserService, policy transit.BoardingWaitPolicy) []transit.UserService {
	out := make([]transit.UserService, len(svcs))
	for i, svc := range svcs {
		out[i] = withUserBoardingWait(svc, policy)
	}
	return out
}
