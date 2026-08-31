package transit

import (
	"math"

	"github.com/andrewcgraves/sparks-effect-api/internal/geo"
)

// modeSpeedKmH is the assumed travel speed for one mode, and the only place a
// TravelMode is turned into a number.
//
// A mode with no entry reports ok=false rather than a zero speed, because a
// zero speed is a reach of zero, which would reject every origin on earth. The
// request validator has already rejected any mode that could land here, so this
// is the guard against a mode being added to TravelMode and forgotten here
// rather than against a caller passing rubbish.
func modeSpeedKmH(m TravelMode) (float64, bool) {
	switch m {
	case TravelModeWalk:
		return geo.WalkSpeedKmH, true
	case TravelModeBike:
		return geo.BikeSpeedKmH, true
	case TravelModeDrive:
		return geo.DriveSpeedKmH, true
	case TravelModeTransit:
		return geo.TransitSpeedKmH, true
	default:
		return 0, false
	}
}

// OriginReach is how one origin stands in relation to a compiled graph's
// stations: the nearest of them, and the furthest the requested mode and budget
// could possibly carry a traveller.
//
// NearestSlug and NearestKm describe the station that came closest, so a caller
// can say *how* far out of range an origin was rather than only that it was.
type OriginReach struct {
	NearestSlug string
	NearestKm   float64
	MaxReachKm  float64
	InRange     bool
}

// CheckOriginReach reports whether any station in the graph is close enough to
// the origin to be worth plotting an isochrone from, and reports ok=false when
// the question cannot be asked at all.
//
// Not asking is the answer for a nil graph, a graph with no nodes, and an
// unknown mode. None of those describe an origin that is too far away — they
// describe a request that is broken in some other way, or a scenario with
// nothing in it — and a check that cannot see any stations must never conclude
// that the origin is out of range of them. Whatever the caller would have done
// with such a request before this check existed, it should still do.
//
// The comparison is a great-circle distance against a straight-line bound (see
// geo.ReachKm), so an origin this reports as out of range is one no street
// network could have connected to any station within the budget. It is
// deliberately more permissive than the routing worker's own destination
// pre-filter: this decides whether to refuse a user, and that decides which
// stations are worth a routing call.
func CheckOriginReach(g *TransitGraph, lat, lng float64, mode TravelMode, budgetMins int) (OriginReach, bool) {
	if g == nil || len(g.Nodes) == 0 {
		return OriginReach{}, false
	}
	speed, ok := modeSpeedKmH(mode)
	if !ok {
		return OriginReach{}, false
	}

	reach := geo.ReachKm(speed, budgetMins)

	nearestKm := math.Inf(1)
	nearestSlug := ""
	for _, n := range g.Nodes {
		if d := geo.HaversineKm(lat, lng, n.Lat, n.Lng); d < nearestKm {
			nearestKm = d
			nearestSlug = n.Slug
		}
	}

	return OriginReach{
		NearestSlug: nearestSlug,
		NearestKm:   nearestKm,
		MaxReachKm:  reach,
		InRange:     nearestKm <= reach,
	}, true
}
