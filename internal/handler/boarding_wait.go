package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// boardingWaitTarget is a service model that can carry the boarding wait
// resolved from its own frequency windows. Both the seeded transit.Service and
// the user-authored transit.UserService satisfy it through a pointer, which is
// what lets one helper fill either instead of a copy per model.
type boardingWaitTarget[T any] interface {
	*T
	ResolveBoardingWait(*transit.BoardingWaitOverride, transit.BoardingWaitPolicy) error
}

// withBoardingWait fills the response-only boarding-wait fields on a service
// from the precedence chain (the service's own override, an optional scenario
// override, then global) and the service's frequency windows.
func withBoardingWait[T any, P boardingWaitTarget[T]](ctx context.Context, svc T, scenario *transit.BoardingWaitOverride, global transit.BoardingWaitPolicy) T {
	if err := P(&svc).ResolveBoardingWait(scenario, global); err != nil {
		// Policy was validated at config load / write time; a resolution fault
		// here would be a programming error, and there is no honest wait to
		// report. Falling back to the default is still better than leaving the
		// fields alone: boarding_wait_secs always serialises while
		// boarding_wait_policy is omitempty, so untouched fields publish a
		// bare 0 s wait under no policy — indistinguishable from a compiled
		// none. The pair stays self-consistent and the log line is what says
		// the policy is wrong.
		slog.ErrorContext(ctx, "handler: boarding wait unresolvable, reporting the default", "error", err)
		// The default charges nothing whatever the windows, so it cannot fail
		// when there is no override in the way. A malformed stored override
		// still fails this call; the log line is what remains.
		_ = P(&svc).ResolveBoardingWait(nil, transit.DefaultBoardingWaitPolicy())
	}
	return svc
}

func withBoardingWaits[T any, P boardingWaitTarget[T]](ctx context.Context, svcs []T, scenario *transit.BoardingWaitOverride, global transit.BoardingWaitPolicy) []T {
	out := make([]T, len(svcs))
	for i, svc := range svcs {
		out[i] = withBoardingWait[T, P](ctx, svc, scenario, global)
	}
	return out
}

func withScenarioBoardingWait(ctx context.Context, sc transit.UserScenario, global transit.BoardingWaitPolicy) transit.UserScenario {
	if err := sc.ResolveBoardingWait(global); err != nil {
		slog.ErrorContext(ctx, "handler: scenario boarding wait unresolvable, reporting the default", "error", err)
		_ = sc.ResolveBoardingWait(transit.DefaultBoardingWaitPolicy())
	}
	return sc
}

func withScenarioBoardingWaits(ctx context.Context, scenarios []transit.UserScenario, global transit.BoardingWaitPolicy) []transit.UserScenario {
	out := make([]transit.UserScenario, len(scenarios))
	for i, sc := range scenarios {
		out[i] = withScenarioBoardingWait(ctx, sc, global)
	}
	return out
}

// resolvedBoardingWaitByService is the map GraphStale compares against: each
// member's policy as the next compile would bake it in.
func resolvedBoardingWaitByService(members []transit.UserService, scenario *transit.BoardingWaitOverride, global transit.BoardingWaitPolicy) map[string]transit.BoardingWaitPolicy {
	out := make(map[string]transit.BoardingWaitPolicy, len(members))
	for _, svc := range members {
		p, _, err := transit.ResolveBoardingWait(svc.BoardingWait, scenario, global)
		if err != nil {
			p = transit.DefaultBoardingWaitPolicy()
		}
		out[svc.ID] = p
	}
	return out
}

// optionalBoardingWait distinguishes an omitted field (leave the stored
// override unchanged) from JSON null (clear it back to inherit). The two are
// not the same on the wire, and a full-replace PUT would otherwise treat omit
// as clear.
type optionalBoardingWait struct {
	set   bool
	raw   []byte
	value *transit.BoardingWaitOverride
}

func (o *optionalBoardingWait) UnmarshalJSON(data []byte) error {
	o.set = true
	o.raw = append([]byte(nil), data...)
	return nil
}

func (o *optionalBoardingWait) parse() error {
	if !o.set {
		return nil
	}
	trimmed := bytes.TrimSpace(o.raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		o.value = nil
		return nil
	}
	var override transit.BoardingWaitOverride
	if err := json.Unmarshal(trimmed, &override); err != nil {
		return fmt.Errorf("boarding_wait: %w", err)
	}
	if _, err := override.Parse(); err != nil {
		return err
	}
	o.value = &override
	return nil
}
