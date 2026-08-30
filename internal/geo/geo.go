// Package geo holds the spherical arithmetic the origin-range check is built
// from: great-circle distance, and the assumed travel speeds it is measured
// against.
//
// It is deliberately a near-copy of sparks-effect-routing-worker's package of
// the same name. See SpeedKmH for why the duplication is accepted rather than
// resolved, and what has to stay true of it.
package geo

import "math"

// earthRadiusKm is the mean radius. The question asked of this package is
// whether a point is tens of kilometres from the nearest station or hundreds,
// so the error a spherical model carries against an ellipsoidal one is orders
// of magnitude below anything that could change the answer.
const earthRadiusKm = 6371.0

// Assumed travel speeds, in km/h, for the modes an isochrone can be plotted
// in.
//
// These are the same numbers as the routing worker's geo package, and they have
// to stay the same numbers. The worker sizes its destination pre-filter with
// them; this repository decides with them whether a request is worth enqueueing
// at all. If the two drift, this side starts rejecting origins the worker would
// have found a station for — a request that used to plot silently stops
// plotting, with nothing in either repository to point at.
//
// They are duplicated rather than shared because the two repositories share no
// Go code by design (see the routing worker's README): the only things that
// cross the boundary are the Postgres schema and one message body. A third
// shared thing would have to be a third hand-maintained copy anyway. What keeps
// the first three honest is that they are physical constants — a walking pace
// and a cycling pace — rather than tuning knobs anyone has a reason to turn.
const (
	WalkSpeedKmH  = 5.0
	BikeSpeedKmH  = 15.0
	DriveSpeedKmH = 80.0
	// TransitSpeedKmH is the one number here that is a decision rather than a
	// physical constant: a blended door-to-door pace for walking plus local
	// transit, including waiting and transfers, chosen in SPA-246's design
	// note. A single bus is slower than this and a commuter rail leg is much
	// faster, so no pace is "the" transit pace the way 5 km/h is walking.
	//
	// That makes it the one speed the two repositories could plausibly want to
	// tune, and so the one that must be changed in both at once. It is the
	// worker's transit pre-filter speed, and this side must never be the
	// smaller of the two: this side refuses a request outright, so a radius
	// tighter than the worker's would reject origins the worker would have
	// found a station for. Equal is what keeps that impossible in both
	// directions — the worker still divides by its detour factor, leaving its
	// own bound strictly inside this one.
	TransitSpeedKmH = 40.0
)

// ReachKm is the furthest a traveller moving at speedKmH could possibly get in
// budgetMins, as a straight line.
//
// It is a bound on what is possible, not an estimate of what is likely, and the
// distinction is the whole point of it. The routing worker divides the same
// product by a detour factor, because a road network is longer than the line it
// follows and a *pre-filter* that admits an unreachable station merely wastes a
// polygon. A *rejection* cannot be built that way: a great-circle distance is a
// floor no street network can undercut, so a point outside this radius is
// provably unreachable, while a point inside it may well be reachable and must
// be allowed through. Applying the detour factor here would reject origins the
// worker would have plotted, which is the one failure this check must not have.
func ReachKm(speedKmH float64, budgetMins int) float64 {
	if budgetMins <= 0 {
		return 0
	}
	return speedKmH * float64(budgetMins) / 60.0
}

// HaversineKm is the great-circle distance between two WGS84 points.
func HaversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	dLat := (lat2 - lat1) * math.Pi / 180
	dLng := (lng2 - lng1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLng/2)*math.Sin(dLng/2)
	return earthRadiusKm * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
