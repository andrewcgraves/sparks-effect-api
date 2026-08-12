package transit

import (
	"fmt"
)

// BoardingWaitKind is how a compile turns frequency windows into the boarding
// wait charged once, at the origin, by graphDijkstra.
//
// The min-across-windows rule (half_headway / full_headway) always selects the
// peak window — the smallest HeadwayS — regardless of time of day. There is no
// departure-time concept on an isochrone request, so a window-aware wait would
// be a larger feature; the optimistic peak reading is deliberate.
type BoardingWaitKind string

const (
	// BoardingWaitNone charges no boarding wait. This is the system default.
	BoardingWaitNone BoardingWaitKind = "none"
	// BoardingWaitHalfHeadway charges min(headway across windows) / 2 — the
	// historical unconditional behaviour.
	BoardingWaitHalfHeadway BoardingWaitKind = "half_headway"
	// BoardingWaitFullHeadway charges min(headway across windows) unhalved.
	BoardingWaitFullHeadway BoardingWaitKind = "full_headway"
	// BoardingWaitFixed charges an explicit non-negative seconds value.
	BoardingWaitFixed BoardingWaitKind = "fixed"
)

// BoardingWaitPolicy is the compile-time input that produces ServiceGraph.WaitSecs.
// Resolution is compile-time so a policy change mints a new compile job and
// invalidates isochrone_cache entries keyed on the old job id for free.
type BoardingWaitPolicy struct {
	Kind BoardingWaitKind
	// FixedSecs is the wait charged when Kind is BoardingWaitFixed. Ignored
	// otherwise. Must be non-negative; validated by ParseBoardingWaitPolicy.
	FixedSecs int
}

// DefaultBoardingWaitPolicy is the unset configuration: no boarding wait.
func DefaultBoardingWaitPolicy() BoardingWaitPolicy {
	return BoardingWaitPolicy{Kind: BoardingWaitNone}
}

// ParseBoardingWaitPolicy validates a kind string and optional fixed-seconds
// companion. An empty kind resolves to none. Unrecognised kinds, a negative
// fixed value, and fixed without companion seconds are rejected — never
// silently remapped onto a non-zero wait.
func ParseBoardingWaitPolicy(kind string, fixedSecs *int) (BoardingWaitPolicy, error) {
	if kind == "" {
		kind = string(BoardingWaitNone)
	}
	switch BoardingWaitKind(kind) {
	case BoardingWaitNone, BoardingWaitHalfHeadway, BoardingWaitFullHeadway:
		return BoardingWaitPolicy{Kind: BoardingWaitKind(kind)}, nil
	case BoardingWaitFixed:
		if fixedSecs == nil {
			return BoardingWaitPolicy{}, fmt.Errorf("boarding wait: policy %q requires a non-negative seconds value", kind)
		}
		if *fixedSecs < 0 {
			return BoardingWaitPolicy{}, fmt.Errorf("boarding wait: fixed seconds must be non-negative, got %d", *fixedSecs)
		}
		return BoardingWaitPolicy{Kind: BoardingWaitFixed, FixedSecs: *fixedSecs}, nil
	default:
		return BoardingWaitPolicy{}, fmt.Errorf("boarding wait: unrecognised policy %q (want none, half_headway, full_headway, or fixed)", kind)
	}
}

// WaitSecs resolves this policy against a service's frequency windows into the
// boarding-wait seconds baked onto ServiceGraph.WaitSecs.
//
// half_headway and full_headway take the minimum HeadwayS across windows (the
// peak window) — documented here deliberately, not as an oversight.
func (p BoardingWaitPolicy) WaitSecs(windows []FrequencyWindow) (int, error) {
	switch p.Kind {
	case BoardingWaitNone, "":
		return 0, nil
	case BoardingWaitHalfHeadway:
		return minHeadway(windows) / 2, nil
	case BoardingWaitFullHeadway:
		return minHeadway(windows), nil
	case BoardingWaitFixed:
		if p.FixedSecs < 0 {
			return 0, fmt.Errorf("boarding wait: fixed seconds must be non-negative, got %d", p.FixedSecs)
		}
		return p.FixedSecs, nil
	default:
		return 0, fmt.Errorf("boarding wait: unrecognised policy %q", p.Kind)
	}
}

func (p BoardingWaitPolicy) kindOrNone() BoardingWaitKind {
	if p.Kind == "" {
		return BoardingWaitNone
	}
	return p.Kind
}

// applyBoardingWait resolves policy against windows onto this service graph.
func (sg *ServiceGraph) applyBoardingWait(policy BoardingWaitPolicy, windows []FrequencyWindow) error {
	wait, err := policy.WaitSecs(windows)
	if err != nil {
		return err
	}
	sg.WaitSecs = wait
	sg.WaitPolicy = string(policy.kindOrNone())
	return nil
}

// ResolveForWindows returns the policy kind and wait seconds that would be
// baked into a ServiceGraph for the given frequency windows. Callers that
// already validated the policy (config.Load) treat a resolution error as
// impossible; it is still surfaced rather than silently remapped.
func (p BoardingWaitPolicy) ResolveForWindows(windows []FrequencyWindow) (kind string, secs int, err error) {
	secs, err = p.WaitSecs(windows)
	if err != nil {
		return "", 0, err
	}
	return string(p.kindOrNone()), secs, nil
}

// minHeadway returns the smallest HeadwayS across windows (the peak window).
// Empty windows yield 0.
func minHeadway(windows []FrequencyWindow) int {
	if len(windows) == 0 {
		return 0
	}
	best := windows[0].HeadwayS
	for _, w := range windows[1:] {
		if w.HeadwayS < best {
			best = w.HeadwayS
		}
	}
	return best
}
