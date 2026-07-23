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
// It is a movement *budget*, and SPA-109's co-located-stop merge has to
// reckon with a stop having spent up to all of it: two stops authored metres
// apart can each snap up to this far and still miss a merge measured only on
// where they landed. SPA-113 settled that by widening the merge radius by
// each stop's OffsetM rather than by shrinking this number — see
// effectiveMergeRadius and MaxMergeRadiusM in cluster.go, the latter of which
// reuses this same 500 m as its ceiling.
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
	chainages := make([]float64, len(snapped))
	for i, sn := range snapped {
		chainages[i] = sn.ChainageM
	}
	if i, backwards, faulty := FirstChainageOrderFault(chainages); faulty {
		from, to := s.Stops[i], s.Stops[i+1]
		relation := "after"
		if backwards {
			relation = "before"
		}
		return fmt.Errorf("stop %q (seq %d) lies %s %q (seq %d) along this route",
			from.Name, from.Seq, relation, to.Name, to.Seq)
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

// FirstChainageOrderFault finds the first place a sequence of chainages
// contradicts the direction the sequence itself established. It reports the
// index i of the earlier stop of the offending pair (so the pair is i, i+1),
// whether the established direction was backwards along the line (which decides
// only how the fault reads), and whether there is a fault at all.
//
// The rule is monotonicity, not ascent, and the distinction is load-bearing:
//
// physics.ProjectStops sorts stops by chainage before building spans, so the
// pattern the compiler builds is the chainage-sorted one whatever order the
// author gave. That is only safe when sorting cannot change which stops are
// adjacent. For a monotonic sequence it cannot: sorting a descending sequence
// reverses it, and reversing a list preserves every adjacent pair. Compiled
// edges are emitted in both directions, each carrying the dwell of the end it
// arrives at, so a reversed span list yields the same graph. A service authored
// east-to-west along a line drawn west-to-east is therefore an ordinary
// service, not a mistake, and rejecting it would make westbound patterns
// unauthorable on an eastward-drawn alignment.
//
// A sequence that doubles back is the case that genuinely breaks: authored
// A→C→B has adjacent pairs {A,C} and {C,B}, while sorting yields A→B→C with
// pairs {A,B} and {B,C}. Different pairs, so a different graph — the service
// says one thing and the compiler builds another, with nothing reporting it.
// That is what this refuses.
//
// Stops at equal chainage neither set nor break the direction. Two stops
// projecting to the same point is its own problem (it compiles to a zero-length
// span), but it is not an ordering disagreement, and reporting it as one would
// name the wrong fault.
//
// It is exported so the snap preview reports against the same rule the write
// path enforces. A preview that called a westbound service out of order would
// send the user to fix something that was never going to be refused.
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

// formatDistance renders a distance for a user-facing message: metres up close,
// kilometres once that stops being readable. A stop 3200 m off the alignment is
// more usefully described as 3.2 km.
func formatDistance(m float64) string {
	if m >= 1000 {
		return fmt.Sprintf("%.1f km", m/1000)
	}
	return fmt.Sprintf("%.0f m", m)
}
