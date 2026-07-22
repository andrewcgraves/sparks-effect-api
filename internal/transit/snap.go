package transit

import (
	"errors"
	"fmt"

	"github.com/andrewcgraves/sparks-effect-api/internal/physics"
)

// OffRouteThresholdM is how far a stop may sit from its route's alignment
// before the write is refused. It is loose enough that a user pinning a station
// building rather than the track centreline is not rejected.
//
// This is the one copy: the snap-stops preview flags at the same distance, and
// a preview that warned at a different distance from the one the save enforced
// would be worse than no warning — the user would fix what it complained about
// and still be refused.
//
// The comparison is strict (offset > threshold), so a stop exactly on the
// boundary saves. The preview draws the boundary the same way.
//
// The number itself is provisional: it is a movement *budget*, and every metre
// of it is a metre of reliability taken from the co-located-stop merge in
// SPA-109. SPA-113 owns that trade-off and may lower this.
const OffRouteThresholdM = 500.0

// ErrRouteGeometry marks a snap that failed because of the stored route rather
// than the submitted stops. A caller mapping this to HTTP should answer 500,
// not 422: the client has done nothing wrong and cannot fix it.
var ErrRouteGeometry = errors.New("route geometry is unusable")

// SnapToRoute projects every stop onto rt's alignment and rewrites it in place
// with where it landed: snapped lat/lng, chainage along the line, and how far
// it moved to get there.
//
// This is what makes a stored stop have one coordinate rather than three. The
// raw authored position is deliberately not retained — the authoring UI holds
// it in its own draft state for as long as the user is looking at it, and after
// that the only position anything should reason about is the one on the line.
// Route geometry is immutable (there is no UpdateRoute), so a persisted snap
// cannot go stale.
//
// It rejects, without mutating the service, when:
//
//   - a stop lands more than OffRouteThresholdM from the alignment, which means
//     the user pointed at somewhere the route does not go; or
//   - chainage runs against the authored sequence, which means the compiler
//     would build a different stopping pattern from the one the service states.
//
// rt must be the route svc references; SnapToRoute does not check that, since
// the caller resolved it. Errors other than ErrRouteGeometry name the offending
// stop and are safe to return to the client verbatim.
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

	for i, sn := range snapped {
		if sn.OffsetM > OffRouteThresholdM {
			return fmt.Errorf("stop %q is %s from route %q",
				s.Stops[i].Name, formatDistance(sn.OffsetM), rt.Slug)
		}
	}
	if err := checkChainageOrder(s.Stops, snapped); err != nil {
		return err
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

// checkChainageOrder reports whether the stops' distances along the line agree
// with the order they were authored in.
//
// The rule is monotonicity, not ascent. A service authored east-to-west along a
// line drawn west-to-east has chainage descending throughout, and that is a
// perfectly ordinary service — the adjacent pairs the compiler builds are
// exactly the authored ones, just walked the other way. What is not ordinary is
// a sequence that doubles back: there the compiler's chainage-sorted pattern
// and the authored pattern genuinely differ, and nothing would report it.
//
// Stops at equal chainage neither set nor break the direction. Two stops
// projecting to the same point on the line is its own problem (it compiles to a
// zero-length span), but it is not an ordering disagreement, and reporting it as
// one would name the wrong fault.
func checkChainageOrder(stops []ServiceStopPoint, snapped []physics.SnappedStop) error {
	ascending := 0 // 0 until a pair with distinct chainage establishes a direction
	for i := 0; i < len(snapped)-1; i++ {
		delta := snapped[i+1].ChainageM - snapped[i].ChainageM
		switch {
		case delta == 0:
			continue
		case ascending == 0:
			if delta > 0 {
				ascending = 1
			} else {
				ascending = -1
			}
		case (delta > 0) != (ascending > 0):
			from, to := stops[i], stops[i+1]
			return fmt.Errorf("stop %q (seq %d) lies after %q (seq %d) along this route",
				from.Name, from.Seq, to.Name, to.Seq)
		}
	}
	return nil
}

// formatDistance renders a distance for a user-facing message: metres up close,
// kilometres once that stops being readable. A stop 3200 m off the alignment is
// more usefully described as 3.2 km.
func formatDistance(m float64) string {
	if m >= 1000 {
		return fmt.Sprintf("%.1f km", m/1000)
	}
	return fmt.Sprintf("%.0f m", m)
}
