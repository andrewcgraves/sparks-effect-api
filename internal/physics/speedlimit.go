// Package physics provides pure, deterministic models that translate track
// geometry and rolling-stock parameters into the kinematic quantities the run
// profile integrator (SPA-79) consumes. Functions here take value inputs and
// return values only; they perform no I/O and hold no state, so they are safe
// to call concurrently and trivial to unit test.
package physics

import "math"

// Physical and comfort constants for the per-point speed-limit model.
//
// The lateral limit uses the classic rail cant-deficiency equilibrium: on a
// curve of radius R with total (applied + deficiency) cant across the running
// gauge, the balancing speed is v = sqrt((cant/gauge) * g * R). Constants below
// pin the model's units and its comfort/safety allowances.
const (
	// gaugeMM is the effective lever arm across which cant is measured, in
	// millimeters. Standard-gauge track is 1435 mm rail-to-rail; the dynamic
	// lever arm (contact-point spacing) is slightly wider, so 1500 mm is used
	// as the effective value in the cant-deficiency relation.
	gaugeMM = 1500.0

	// gravityMS2 is standard gravitational acceleration, in m/s^2.
	gravityMS2 = 9.81

	// maxCantDeficiencyMM is the comfort/safety ceiling on cant deficiency, in
	// millimeters. It is the unbalanced lateral cant the model permits beyond
	// the physically applied cant — i.e. the passenger-comfort lateral
	// acceleration allowance expressed in cant units. 150 mm is a common
	// higher-speed rail comfort limit.
	maxCantDeficiencyMM = 150.0

	// gradeDerateThreshold is the descending-grade ratio (rise/run) below which
	// no speed reduction is applied. Descents gentler than 2% do not meaningfully
	// stress braking distance in this first-pass model.
	gradeDerateThreshold = 0.02

	// gradeDerateCoeff is the fractional speed reduction applied per unit of
	// descending grade beyond gradeDerateThreshold. A first-pass braking-distance
	// placeholder: at 5.0, an extra 10% of grade removes 50% of the speed before
	// the floor engages.
	gradeDerateCoeff = 5.0

	// minGradeFactor is the floor on the descending-grade derate factor, so even
	// very steep descents retain at least half the geometric/vehicle limit.
	minGradeFactor = 0.5

	// msToKMH converts meters per second to kilometers per hour.
	msToKMH = 3.6
)

// SpeedLimitInputs are the per-point track physics and the vehicle ceiling
// used to compute an allowable cruise speed.
type SpeedLimitInputs struct {
	CurveRadiusM  float64 // curve radius, meters; <= 0 or +Inf means tangent (straight) track — no lateral limit
	AppliedCantMM float64 // applied superelevation (cant), millimeters
	Grade         float64 // grade as a ratio rise/run; positive = ascending, negative = descending
	VehicleMaxKMH float64 // service-defined vehicle maximum speed, km/h
}

// SpeedLimit returns the allowable cruise speed in km/h at a point, given track
// geometry and the vehicle ceiling.
//
// The model has three stages:
//
//  1. Lateral limit — a curve of radius R with effective cant (applied cant plus
//     the maxCantDeficiencyMM comfort allowance) admits a balancing speed of
//     v = sqrt((effectiveCant/gauge) * g * R). Tangent track (radius <= 0 or
//     +Inf) imposes no lateral limit.
//  2. Vehicle ceiling — the lateral limit is capped at VehicleMaxKMH.
//  3. Grade derate — descending grades steeper than gradeDerateThreshold scale
//     the result down by a conservative braking-distance factor, floored at
//     minGradeFactor. Ascending or level grades are not derated here; their
//     achievable-speed effects are handled downstream in the profile
//     integration (SPA-79). This derate is an explicit first-pass placeholder.
//
// The returned speed NEVER exceeds VehicleMaxKMH. Degenerate inputs are handled
// defensively: a non-positive VehicleMaxKMH returns 0, and a non-positive or
// non-finite radius is treated as tangent track.
func SpeedLimit(in SpeedLimitInputs) float64 {
	// A vehicle with no positive ceiling cannot move; nothing else matters.
	if !(in.VehicleMaxKMH > 0) { // also catches NaN
		return 0
	}

	// Stage 1: lateral (curve + cant) limit.
	vCurveKMH := math.Inf(1) // tangent track: no lateral limit
	if isCurved(in.CurveRadiusM) {
		effectiveCantMM := in.AppliedCantMM + maxCantDeficiencyMM
		if effectiveCantMM < 0 {
			// Extreme negative applied cant would make the radicand negative;
			// clamp to zero so the curve simply forbids any speed rather than
			// producing NaN.
			effectiveCantMM = 0
		}
		vCurveMS := math.Sqrt((effectiveCantMM / gaugeMM) * gravityMS2 * in.CurveRadiusM)
		vCurveKMH = vCurveMS * msToKMH
	}

	// Stage 2: combine the geometric limit with the vehicle ceiling.
	speed := math.Min(vCurveKMH, in.VehicleMaxKMH)

	// Stage 3: descending-grade derate.
	speed *= gradeFactor(in.Grade)

	// Final clamp: guarantee the result never exceeds the vehicle max (AC #2)
	// and is a sane, non-negative number.
	speed = math.Min(speed, in.VehicleMaxKMH)
	if math.IsNaN(speed) || speed < 0 {
		return 0
	}
	return speed
}

// isCurved reports whether a radius represents real curvature that imposes a
// lateral speed limit. Non-positive and non-finite radii are treated as tangent
// (straight) track.
func isCurved(radiusM float64) bool {
	return radiusM > 0 && !math.IsInf(radiusM, 1)
}

// gradeFactor returns the multiplicative speed derate for a grade ratio.
// Ascending or level grades (>= 0) return 1.0. Descending grades gentler than
// gradeDerateThreshold also return 1.0; steeper descents scale down linearly
// with grade, floored at minGradeFactor.
func gradeFactor(grade float64) float64 {
	if math.IsNaN(grade) || grade >= 0 {
		return 1.0
	}
	d := -grade // depth of descent, positive
	if d <= gradeDerateThreshold {
		return 1.0
	}
	return math.Max(minGradeFactor, 1-gradeDerateCoeff*(d-gradeDerateThreshold))
}
