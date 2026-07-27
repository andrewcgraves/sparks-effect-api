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

// StopPlacementFaultKind names which placement rule a submitted service broke.
// The values are the wire contract a client branches on, so they are stable
// strings rather than an integer enum whose meaning depends on declaration
// order.
type StopPlacementFaultKind string

const (
	// OffRouteFault is a stop further than OffRouteThresholdM from the
	// alignment. One stop is at fault.
	OffRouteFault StopPlacementFaultKind = "off_route"
	// ChainageOrderFault is an authored sequence that doubles back along the
	// line — see FirstChainageOrderFault. Two adjacent stops are at fault.
	ChainageOrderFault StopPlacementFaultKind = "chainage_order"
)

// FaultedStop identifies one stop a fault is about, and where it landed.
//
// All three identity fields are reported because a client may hold any of
// them: an authoring UI mid-create knows the seq and the name it typed but not
// the slug, which the server mints, while anything working from a stored
// service has the slug. Naming the stop three ways costs nothing and means no
// caller has to reconstruct an identity it was not given.
type FaultedStop struct {
	Seq       int
	Name      string
	Slug      string
	ChainageM float64
	OffsetM   float64
}

// StopPlacementFault is a refusal SnapToRoute can attribute to specific stops,
// as opposed to ErrRouteGeometry, which is the stored route's fault.
//
// It exists so the placement rules have a machine-readable form. The prose from
// Error() remains what a user reads, but it is no longer the contract: SPA-146
// recovered stop identities from that wording with regular expressions, which
// made every rewording a silent break of the authoring UI's per-stop feedback.
//
// A caller mapping this to HTTP should answer 422 — the client submitted stops
// it can move.
type StopPlacementFault struct {
	Kind      StopPlacementFaultKind
	RouteSlug string
	// ThresholdM is the distance the fault was measured against. It is echoed
	// for OffRouteFault only, so a client renders the boundary the server
	// applied rather than keeping its own copy of the number.
	ThresholdM float64
	// Backwards records that the sequence had established a descending
	// direction along the line before it doubled back. It decides only how the
	// message reads — running against the drawn direction is not itself a
	// fault — and is deliberately absent from the wire contract.
	Backwards bool
	// Stops are the offending stops in authored order: one for OffRouteFault,
	// the adjacent pair for ChainageOrderFault.
	Stops []FaultedStop
}

func (f *StopPlacementFault) Error() string {
	switch f.Kind {
	case ChainageOrderFault:
		from, to := f.Stops[0], f.Stops[1]
		relation := "after"
		if f.Backwards {
			relation = "before"
		}
		return fmt.Sprintf("stop %q (seq %d) lies %s %q (seq %d) along this route",
			from.Name, from.Seq, relation, to.Name, to.Seq)
	default:
		stop := f.Stops[0]
		return fmt.Sprintf("stop %q is %s from route %q",
			stop.Name, formatDistance(stop.OffsetM), f.RouteSlug)
	}
}

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
// the caller resolved it. Both refusals come back as a *StopPlacementFault,
// which names the offending stops in fields and whose message is safe to return
// to the client verbatim. Only ErrRouteGeometry is anything else.
//
// Stop identity in the fault is whatever svc carries, so a caller that wants
// slugs in it must have minted them (MintStopSlugs) first — as the write path
// does, well before it snaps.
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
