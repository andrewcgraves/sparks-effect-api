package handler

import (
	"context"
	"log/slog"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// boardingWaitTarget is a service model that can carry the boarding wait
// resolved from its own frequency windows. Both the seeded transit.Service and
// the user-authored transit.UserService satisfy it through a pointer, which is
// what lets one helper fill either instead of a copy per model.
type boardingWaitTarget[T any] interface {
	*T
	ResolveBoardingWait(transit.BoardingWaitPolicy) error
}

// withBoardingWait fills the response-only boarding-wait fields on a service
// from the global policy and the service's frequency windows.
func withBoardingWait[T any, P boardingWaitTarget[T]](ctx context.Context, svc T, policy transit.BoardingWaitPolicy) T {
	if err := P(&svc).ResolveBoardingWait(policy); err != nil {
		// Policy was validated at config load; a resolution fault here would be
		// a programming error, and there is no honest wait to report. Falling
		// back to the default is still better than leaving the fields alone:
		// boarding_wait_secs always serialises while boarding_wait_policy is
		// omitempty, so untouched fields publish a bare 0 s wait under no
		// policy — indistinguishable from a compiled none. The pair stays
		// self-consistent and the log line is what says the policy is wrong.
		slog.ErrorContext(ctx, "handler: boarding wait unresolvable, reporting the default", "error", err)
		// The default charges nothing whatever the windows, so it cannot fail.
		_ = P(&svc).ResolveBoardingWait(transit.DefaultBoardingWaitPolicy())
	}
	return svc
}

func withBoardingWaits[T any, P boardingWaitTarget[T]](ctx context.Context, svcs []T, policy transit.BoardingWaitPolicy) []T {
	out := make([]T, len(svcs))
	for i, svc := range svcs {
		out[i] = withBoardingWait[T, P](ctx, svc, policy)
	}
	return out
}
