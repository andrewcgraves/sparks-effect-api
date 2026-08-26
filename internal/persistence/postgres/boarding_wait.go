package postgres

import "github.com/andrewcgraves/sparks-effect-api/internal/transit"

// boardingWaitArgs splits a stored override into the two nullable columns.
// A nil override is inherit: both columns NULL. fixed is the only kind that
// writes seconds; the others leave the companion NULL so a leftover number
// cannot be mistaken for a deliberate wait.
func boardingWaitArgs(o *transit.BoardingWaitOverride) (kind, secs any) {
	if o == nil {
		return nil, nil
	}
	kind = string(o.Policy)
	if o.Policy == transit.BoardingWaitFixed && o.Secs != nil {
		return kind, *o.Secs
	}
	return kind, nil
}

// scanBoardingWait rebuilds a stored override from the two nullable columns.
// A NULL policy is inherit.
func scanBoardingWait(kind *string, secs *int) *transit.BoardingWaitOverride {
	if kind == nil || *kind == "" {
		return nil
	}
	return &transit.BoardingWaitOverride{
		Policy: transit.BoardingWaitKind(*kind),
		Secs:   secs,
	}
}
