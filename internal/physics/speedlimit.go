package physics

import "math"

const (
	gaugeMM                    = 1500.0
	gravityMS2                 = 9.81
	defaultMaxCantDeficiencyMM = 150.0
	gradeDerateThreshold       = 0.02
	gradeDerateCoeff           = 5.0
	minGradeFactor             = 0.5
	msToKMH                    = 3.6
)

type SpeedLimitInputs struct {
	CurveRadiusM        float64
	AppliedCantMM       float64
	MaxCantDeficiencyMM float64
	Grade               float64
	VehicleMaxKMH       float64
}

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

func isCurved(radiusM float64) bool {
	return radiusM > 0 && !math.IsInf(radiusM, 1)
}

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
