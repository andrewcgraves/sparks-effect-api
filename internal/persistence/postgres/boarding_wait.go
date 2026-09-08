package postgres

import "github.com/andrewcgraves/sparks-effect-api/internal/transit"

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

func scanBoardingWait(kind *string, secs *int) *transit.BoardingWaitOverride {
	if kind == nil || *kind == "" {
		return nil
	}
	return &transit.BoardingWaitOverride{
		Policy: transit.BoardingWaitKind(*kind),
		Secs:   secs,
	}
}
