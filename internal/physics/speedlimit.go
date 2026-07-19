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

	// defaultMaxCantDeficiencyMM is the fallback comfort/safety ceiling on cant
	// deficiency, in millimeters, used when the caller does not supply a
	// vehicle-specific value via SpeedLimitInputs.MaxCantDeficiencyMM. It is the
	// unbalanced lateral cant the model permits beyond the physically applied
	// cant — i.e. the passenger-comfort lateral acceleration allowance expressed
	// in cant units. 150 mm is a common higher-speed rail comfort limit.
	//
	// Permissible cant deficiency depends on what is running (tilting trains
	// tolerate much more), so it is a per-vehicle input; this constant only
	// covers the unset case.
	defaultMaxCantDeficiencyMM = 150.0

	// gradeDerateThreshold is the descending-grade ratio (rise/run) at or below
	// which no speed reduction is applied. Descents gentler than 2% do not
	// meaningfully stress braking distance in this first-pass model.
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

// SpeedLimitInputs are the per-point track physics and the vehicle parameters
// used to compute an allowable cruise speed.
//
// Values are normalized defensively by SpeedLimit; see that function for how
// out-of-range or non-finite fields are handled.
type SpeedLimitInputs struct {
	CurveRadiusM  float64 // curve radius, meters; <= 0 or +Inf means tangent (straight) track — no lateral limit
	AppliedCantMM float64 // applied superelevation (cant), millimeters; negative/NaN is treated as 0

	// MaxCantDeficiencyMM is the vehicle's permissible cant deficiency, in
	// millimeters — the unbalanced lateral cant it may run. It is per-vehicle
	// because tilting stock tolerates far more than conventional stock. A value
	// <= 0 (or NaN) falls back to defaultMaxCantDeficiencyMM.
	MaxCantDeficiencyMM float64

	Grade         float64 // grade as a ratio rise/run; positive = ascending, negative = descending; NaN treated as level
	VehicleMaxKMH float64 // service-defined vehicle maximum speed, km/h
}

// SpeedLimit returns the allowable cruise speed in km/h at a point, given track
// geometry and the vehicle parameters.
//
// The model has three stages:
//
//  1. Lateral limit — a curve of radius R with effective cant (applied cant plus
//     the vehicle's cant-deficiency allowance) admits a balancing speed of
//     v = sqrt((effectiveCant/gauge) * g * R). Tangent track (radius <= 0 or
//     +Inf) imposes no lateral limit.
//  2. Vehicle ceiling — the lateral limit is capped at VehicleMaxKMH. This is the
//     only place the vehicle max is enforced.
//  3. Grade derate — descending grades steeper than gradeDerateThreshold scale
//     the result down by a conservative braking-distance factor in
//     [minGradeFactor, 1], floored at minGradeFactor. Because this factor is
//     never greater than 1, it can only reduce the stage-2 result, so no
//     re-clamp against VehicleMaxKMH is needed afterwards. Ascending or level
//     grades are not derated here; their achievable-speed effects are handled
//     downstream in the profile integration (SPA-79). This derate is an explicit
//     first-pass placeholder.
//
// The returned speed is always in the range [0, VehicleMaxKMH] and is finite.
// Inputs are normalized defensively before use: a non-positive or NaN
// VehicleMaxKMH returns 0; a non-positive or non-finite radius is treated as
// tangent track; a negative or NaN applied cant is treated as 0; a non-positive
// or NaN cant deficiency falls back to defaultMaxCantDeficiencyMM; and a NaN
// grade is treated as level.
func SpeedLimit(in SpeedLimitInputs) float64 {
	// A vehicle with no positive, finite ceiling cannot move (or is invalid), so
	// there is no meaningful limit to return. This rejects <= 0, NaN
	// (VehicleMaxKMH > 0 is false for NaN), and +Inf (which would otherwise break
	// the finite [0, vmax] guarantee on tangent track).
	if !(in.VehicleMaxKMH > 0) || math.IsInf(in.VehicleMaxKMH, 1) {
		return 0
	}

	// Normalize the track-physics inputs, guarding against invalid/degenerate
	// values so the arithmetic below cannot produce NaN or a negative radicand.
	appliedCantMM := in.AppliedCantMM
	if math.IsNaN(appliedCantMM) || appliedCantMM < 0 {
		// Negative/NaN applied cant is not modeled (no adverse-cant handling in
		// this first pass); treat as zero applied cant.
		appliedCantMM = 0
	}

	cantDeficiencyMM := in.MaxCantDeficiencyMM
	if math.IsNaN(cantDeficiencyMM) || cantDeficiencyMM <= 0 {
		cantDeficiencyMM = defaultMaxCantDeficiencyMM
	}

	grade := in.Grade
	if math.IsNaN(grade) {
		grade = 0 // unknown grade: treat as level, no derate
	}

	// Stage 1: lateral (curve + cant) limit.
	vCurveKMH := math.Inf(1) // tangent track: no lateral limit
	if isCurved(in.CurveRadiusM) {
		effectiveCantMM := appliedCantMM + cantDeficiencyMM
		vCurveMS := math.Sqrt((effectiveCantMM / gaugeMM) * gravityMS2 * in.CurveRadiusM)
		vCurveKMH = vCurveMS * msToKMH
	}

	// Stage 2: apply the vehicle ceiling. Because VehicleMaxKMH is finite and
	// positive, speed is now in [0, VehicleMaxKMH].
	speed := math.Min(vCurveKMH, in.VehicleMaxKMH)

	// Stage 3: descending-grade derate. gradeFactor is in [minGradeFactor, 1],
	// so this only ever reduces speed and keeps it within [0, VehicleMaxKMH];
	// no further clamp is required.
	speed *= gradeFactor(grade)

	return speed
}

// isCurved reports whether a radius represents real curvature that imposes a
// lateral speed limit. Non-positive and non-finite radii are treated as tangent
// (straight) track. (NaN > 0 is false, so NaN radii are tangent too.)
func isCurved(radiusM float64) bool {
	return radiusM > 0 && !math.IsInf(radiusM, 1)
}

// gradeFactor returns the multiplicative speed derate for a grade ratio.
// Ascending or level grades (>= 0) return 1.0. Descending grades at or gentler
// than gradeDerateThreshold also return 1.0; steeper descents scale down
// linearly with grade, floored at minGradeFactor. The result is always in
// [minGradeFactor, 1].
func gradeFactor(grade float64) float64 {
	if grade >= 0 { // ascending or level (NaN is normalized to 0 by the caller)
		return 1.0
	}
	d := -grade // depth of descent, positive
	if d <= gradeDerateThreshold {
		return 1.0
	}
	return math.Max(minGradeFactor, 1-gradeDerateCoeff*(d-gradeDerateThreshold))
}
