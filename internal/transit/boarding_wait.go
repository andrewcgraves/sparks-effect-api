package transit

import (
	"fmt"
)

type BoardingWaitKind string

const (
	BoardingWaitNone        BoardingWaitKind = "none"
	BoardingWaitHalfHeadway BoardingWaitKind = "half_headway"
	BoardingWaitFullHeadway BoardingWaitKind = "full_headway"
	BoardingWaitFixed       BoardingWaitKind = "fixed"
)

type BoardingWaitPolicy struct {
	Kind      BoardingWaitKind
	FixedSecs int
}

type BoardingWaitOverride struct {
	Policy BoardingWaitKind `json:"policy" yaml:"policy"`
	Secs   *int             `json:"secs,omitempty" yaml:"secs,omitempty"`
}

const (
	BoardingWaitSourceService  = "service"
	BoardingWaitSourceScenario = "scenario"
	BoardingWaitSourceGlobal   = "global"
)

func (o BoardingWaitOverride) Parse() (BoardingWaitPolicy, error) {
	if o.Policy == "" {
		return BoardingWaitPolicy{}, fmt.Errorf("boarding wait: policy is required")
	}
	return ParseBoardingWaitPolicy(string(o.Policy), o.Secs)
}

func ResolveBoardingWait(service, scenario *BoardingWaitOverride, global BoardingWaitPolicy) (BoardingWaitPolicy, string, error) {
	if service != nil {
		p, err := service.Parse()
		return p, BoardingWaitSourceService, err
	}
	if scenario != nil {
		p, err := scenario.Parse()
		return p, BoardingWaitSourceScenario, err
	}
	return global, BoardingWaitSourceGlobal, nil
}

func DefaultBoardingWaitPolicy() BoardingWaitPolicy {
	return BoardingWaitPolicy{Kind: BoardingWaitNone}
}

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

func (p BoardingWaitPolicy) resolveInto(windows []FrequencyWindow, kind *string, secs *int) error {
	wait, err := p.WaitSecs(windows)
	if err != nil {
		return err
	}
	*kind, *secs = string(p.kindOrNone()), wait
	return nil
}

func applyBoardingWait(sg *ServiceGraph, policy BoardingWaitPolicy, windows []FrequencyWindow) error {
	return policy.resolveInto(windows, &sg.WaitPolicy, &sg.WaitSecs)
}

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
