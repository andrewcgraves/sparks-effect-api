package transit

import (
	"errors"
	"fmt"

	"github.com/andrewcgraves/sparks-effect-api/internal/physics"
)

const OffRouteThresholdM = 500.0

var ErrRouteGeometry = errors.New("route geometry is unusable")

type StopPlacementFaultKind string

const (
	OffRouteFault      StopPlacementFaultKind = "off_route"
	ChainageOrderFault StopPlacementFaultKind = "chainage_order"
)

type FaultedStop struct {
	Seq       int     `json:"seq"`
	Name      string  `json:"name"`
	Slug      string  `json:"slug"`
	ChainageM float64 `json:"chainage_m"`
	OffsetM   float64 `json:"offset_m"`
}

type StopPlacementFault struct {
	Kind       StopPlacementFaultKind
	RouteSlug  string
	ThresholdM float64
	Backwards  bool
	Stops      []FaultedStop
}

func (f *StopPlacementFault) Error() string {
	switch {
	case f.Kind == OffRouteFault && len(f.Stops) >= 1:
		stop := f.Stops[0]
		return fmt.Sprintf("stop %q is %s from route %q",
			stop.Name, formatDistance(stop.OffsetM), f.RouteSlug)
	case f.Kind == ChainageOrderFault && len(f.Stops) >= 2:
		from, to := f.Stops[0], f.Stops[1]
		relation := "after"
		if f.Backwards {
			relation = "before"
		}
		return fmt.Sprintf("stop %q (seq %d) lies %s %q (seq %d) along this route",
			from.Name, from.Seq, relation, to.Name, to.Seq)
	default:
		return fmt.Sprintf("stops are placed invalidly on route %q", f.RouteSlug)
	}
}

func (s *UserService) SnapToRoute(rt Route) error {
	line, err := ToPhysicsLine(rt.Geometry)
	if err != nil {
		return fmt.Errorf("%w: route %q: %w", ErrRouteGeometry, rt.Slug, err)
	}

	stops := make([]physics.Stop, len(s.Stops))
	for i, stop := range s.Stops {
		stops[i] = physics.Stop{ID: stop.Name, Location: physics.Point{Lng: stop.Lng, Lat: stop.Lat}}
	}

	// SnapStops preserves input order, so results are index-aligned with
	// s.Stops — which is what makes the order check below possible at all.
	snapped, err := physics.SnapStops(line, stops)
	if err != nil {
		return fmt.Errorf("%w: route %q: %w", ErrRouteGeometry, rt.Slug, err)
	}

	faultedStop := func(i int) FaultedStop {
		return FaultedStop{
			Seq:       s.Stops[i].Seq,
			Name:      s.Stops[i].Name,
			Slug:      s.Stops[i].Slug,
			ChainageM: snapped[i].ChainageM,
			OffsetM:   snapped[i].OffsetM,
		}
	}

	for i, sn := range snapped {
		if sn.OffsetM > OffRouteThresholdM {
			return &StopPlacementFault{
				Kind:       OffRouteFault,
				RouteSlug:  rt.Slug,
				ThresholdM: OffRouteThresholdM,
				Stops:      []FaultedStop{faultedStop(i)},
			}
		}
	}
	chainages := make([]float64, len(snapped))
	for i, sn := range snapped {
		chainages[i] = sn.ChainageM
	}
	if i, backwards, faulty := FirstChainageOrderFault(chainages); faulty {
		return &StopPlacementFault{
			Kind:      ChainageOrderFault,
			RouteSlug: rt.Slug,
			Backwards: backwards,
			Stops:     []FaultedStop{faultedStop(i), faultedStop(i + 1)},
		}
	}

	// Every check has passed, so committing the rewrite cannot leave the
	// service half-snapped.
	for i, sn := range snapped {
		s.Stops[i].Lat = sn.Point.Lat
		s.Stops[i].Lng = sn.Point.Lng
		s.Stops[i].ChainageM = sn.ChainageM
		s.Stops[i].OffsetM = sn.OffsetM
	}
	return nil
}

func FirstChainageOrderFault(chainageM []float64) (i int, backwards, faulty bool) {
	direction := 0 // 0 until a pair with distinct chainage establishes one
	for i := 0; i < len(chainageM)-1; i++ {
		delta := chainageM[i+1] - chainageM[i]
		switch {
		case delta == 0:
			continue
		case direction == 0:
			if delta > 0 {
				direction = 1
			} else {
				direction = -1
			}
		case (delta > 0) != (direction > 0):
			return i, direction < 0, true
		}
	}
	return 0, false, false
}

func formatDistance(m float64) string {
	if m >= 1000 {
		return fmt.Sprintf("%.1f km", m/1000)
	}
	return fmt.Sprintf("%.0f m", m)
}
