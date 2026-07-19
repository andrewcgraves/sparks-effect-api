package physics

import (
	"math"
	"testing"
)

// speedTol is the absolute tolerance (km/h) used when comparing computed
// speeds. The model is deterministic, so a tight tolerance suffices; it only
// guards against floating-point representation noise.
const speedTol = 1e-6

// almostEqual reports whether a and b are within speedTol of each other.
func almostEqual(a, b float64) bool {
	return math.Abs(a-b) <= speedTol
}

// almostEqualTol reports whether a and b are within an explicit tolerance,
// used by golden-value assertions that compare against a hand-rounded literal.
func almostEqualTol(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func TestSpeedLimit_tangentLevelReturnsVehicleMax(t *testing.T) {
	const vmax = 320.0
	got := SpeedLimit(SpeedLimitInputs{
		CurveRadiusM:  0, // tangent
		AppliedCantMM: 0,
		Grade:         0,
		VehicleMaxKMH: vmax,
	})
	if !almostEqual(got, vmax) {
		t.Errorf("tangent/level: got %v, want %v", got, vmax)
	}
}

func TestSpeedLimit_infiniteRadiusIsTangent(t *testing.T) {
	const vmax = 200.0
	got := SpeedLimit(SpeedLimitInputs{
		CurveRadiusM:  math.Inf(1),
		AppliedCantMM: 0,
		Grade:         0,
		VehicleMaxKMH: vmax,
	})
	if !almostEqual(got, vmax) {
		t.Errorf("+Inf radius: got %v, want %v", got, vmax)
	}
}

func TestSpeedLimit_tightCurveWellBelowVehicleMax(t *testing.T) {
	const vmax = 320.0
	got := SpeedLimit(SpeedLimitInputs{
		CurveRadiusM:  150, // tight curve
		AppliedCantMM: 0,
		Grade:         0,
		VehicleMaxKMH: vmax,
	})
	if got >= vmax {
		t.Errorf("tight curve: got %v, expected well below vehicle max %v", got, vmax)
	}
	if got <= 0 {
		t.Errorf("tight curve: got %v, expected a positive limit", got)
	}
}

// TestSpeedLimit_goldenValue pins the numeric output of the lateral model for a
// documented worked example, so a change to the formula or a constant is caught
// rather than silently altering computed speeds.
//
// Worked example: R = 400 m, applied cant = 150 mm, default deficiency = 150 mm,
// gauge = 1500 mm, g = 9.81 m/s^2, vehicle max well above the geometric limit.
//
//	effectiveCant = 150 + 150            = 300 mm
//	v = sqrt((300/1500) * 9.81 * 400)    = sqrt(784.8) = 28.01428 m/s
//	v * 3.6                              = 100.8514 km/h
//
// NOTE: this pins *this model's* output, not an externally published figure.
// Validating the model against a standard (AREMA / EN 13803) is deferred to a
// later story per review — see SPA-78 discussion.
func TestSpeedLimit_goldenValue(t *testing.T) {
	const (
		want = 100.8514 // km/h, hand-computed above
		tol  = 1e-3
	)
	got := SpeedLimit(SpeedLimitInputs{
		CurveRadiusM:  400,
		AppliedCantMM: 150,
		Grade:         0,
		VehicleMaxKMH: 1000,
	})
	if !almostEqualTol(got, want, tol) {
		t.Errorf("golden value: got %v, want %v (±%v)", got, want, tol)
	}
}

func TestSpeedLimit_monotonicInRadius(t *testing.T) {
	const vmax = 1000.0 // high ceiling so the geometric limit dominates
	radii := []float64{100, 200, 400, 800, 1600, 3200}
	prev := math.Inf(-1)
	for _, r := range radii {
		got := SpeedLimit(SpeedLimitInputs{
			CurveRadiusM:  r,
			AppliedCantMM: 0,
			Grade:         0,
			VehicleMaxKMH: vmax,
		})
		if got < prev {
			t.Errorf("radius %v: got %v, expected >= previous %v (monotonic in radius)", r, got, prev)
		}
		prev = got
	}
}

func TestSpeedLimit_monotonicInCant(t *testing.T) {
	const (
		vmax   = 1000.0 // high ceiling so the geometric limit dominates
		radius = 400.0
	)
	cants := []float64{0, 50, 100, 150, 200}
	prev := math.Inf(-1)
	for _, c := range cants {
		got := SpeedLimit(SpeedLimitInputs{
			CurveRadiusM:  radius,
			AppliedCantMM: c,
			Grade:         0,
			VehicleMaxKMH: vmax,
		})
		if got < prev {
			t.Errorf("cant %v: got %v, expected >= previous %v (monotonic in cant)", c, got, prev)
		}
		prev = got
	}
}

func TestSpeedLimit_addingCantRaisesSpeed(t *testing.T) {
	const (
		vmax   = 1000.0
		radius = 400.0
	)
	noCant := SpeedLimit(SpeedLimitInputs{CurveRadiusM: radius, AppliedCantMM: 0, VehicleMaxKMH: vmax})
	withCant := SpeedLimit(SpeedLimitInputs{CurveRadiusM: radius, AppliedCantMM: 150, VehicleMaxKMH: vmax})
	if !(withCant > noCant) {
		t.Errorf("adding cant: got %v with cant vs %v without, expected an increase", withCant, noCant)
	}
}

// TestSpeedLimit_higherCantDeficiencyRaisesSpeed covers the per-vehicle cant
// deficiency: a vehicle allowed a larger deficiency may take the same curve
// faster. A zero/unset deficiency falls back to the default.
func TestSpeedLimit_higherCantDeficiencyRaisesSpeed(t *testing.T) {
	const (
		vmax   = 1000.0
		radius = 400.0
	)
	base := SpeedLimit(SpeedLimitInputs{CurveRadiusM: radius, AppliedCantMM: 100, VehicleMaxKMH: vmax})
	deflt := SpeedLimit(SpeedLimitInputs{CurveRadiusM: radius, AppliedCantMM: 100, MaxCantDeficiencyMM: 0, VehicleMaxKMH: vmax})
	generous := SpeedLimit(SpeedLimitInputs{CurveRadiusM: radius, AppliedCantMM: 100, MaxCantDeficiencyMM: 300, VehicleMaxKMH: vmax})

	if !almostEqual(deflt, base) {
		t.Errorf("zero deficiency should fall back to default: got %v, want %v", deflt, base)
	}
	if !(generous > base) {
		t.Errorf("higher deficiency: got %v, expected above default-deficiency %v", generous, base)
	}
}

func TestSpeedLimit_largeRadiusClampedToVehicleMax(t *testing.T) {
	const vmax = 120.0
	// A very large radius produces a geometric limit far above vmax.
	got := SpeedLimit(SpeedLimitInputs{
		CurveRadiusM:  100000,
		AppliedCantMM: 150,
		Grade:         0,
		VehicleMaxKMH: vmax,
	})
	if !almostEqual(got, vmax) {
		t.Errorf("large radius: got %v, want clamp to vehicle max %v", got, vmax)
	}
}

func TestSpeedLimit_steepDescentReducesSpeed(t *testing.T) {
	const vmax = 200.0
	level := SpeedLimit(SpeedLimitInputs{CurveRadiusM: 0, Grade: 0, VehicleMaxKMH: vmax})
	descent := SpeedLimit(SpeedLimitInputs{CurveRadiusM: 0, Grade: -0.05, VehicleMaxKMH: vmax})
	if !(descent < level) {
		t.Errorf("steep descent: got %v, expected below level-grade %v", descent, level)
	}
}

func TestSpeedLimit_descentDerateFlooredAtMinFactor(t *testing.T) {
	const vmax = 200.0
	// A very steep descent should saturate the derate at minGradeFactor.
	got := SpeedLimit(SpeedLimitInputs{CurveRadiusM: 0, Grade: -0.9, VehicleMaxKMH: vmax})
	want := vmax * minGradeFactor
	if !almostEqual(got, want) {
		t.Errorf("very steep descent: got %v, want floored %v (minGradeFactor=%v)", got, want, minGradeFactor)
	}
}

// TestSpeedLimit_descentThresholdBoundary pins the behavior exactly at the
// derate threshold and just past it: a descent at -gradeDerateThreshold is not
// derated, while a marginally steeper descent is.
func TestSpeedLimit_descentThresholdBoundary(t *testing.T) {
	const vmax = 200.0

	atThreshold := SpeedLimit(SpeedLimitInputs{CurveRadiusM: 0, Grade: -gradeDerateThreshold, VehicleMaxKMH: vmax})
	if !almostEqual(atThreshold, vmax) {
		t.Errorf("descent exactly at threshold: got %v, want %v (no derate)", atThreshold, vmax)
	}

	// Just past the threshold, the derate engages, so speed drops below vmax.
	justPast := SpeedLimit(SpeedLimitInputs{CurveRadiusM: 0, Grade: -(gradeDerateThreshold + 0.01), VehicleMaxKMH: vmax})
	if !(justPast < vmax) {
		t.Errorf("descent just past threshold: got %v, expected below %v", justPast, vmax)
	}
	// The expected factor is 1 - coeff*(d - threshold) with d = threshold + 0.01.
	wantFactor := 1 - gradeDerateCoeff*0.01
	if !almostEqual(justPast, vmax*wantFactor) {
		t.Errorf("descent just past threshold: got %v, want %v", justPast, vmax*wantFactor)
	}
}

func TestSpeedLimit_gentleDescentNotDerated(t *testing.T) {
	const vmax = 200.0
	// A descent gentler than the threshold must not be derated.
	got := SpeedLimit(SpeedLimitInputs{CurveRadiusM: 0, Grade: -(gradeDerateThreshold / 2), VehicleMaxKMH: vmax})
	if !almostEqual(got, vmax) {
		t.Errorf("gentle descent: got %v, want %v (no derate)", got, vmax)
	}
}

func TestSpeedLimit_ascendingEqualsLevel(t *testing.T) {
	const vmax = 200.0
	level := SpeedLimit(SpeedLimitInputs{CurveRadiusM: 0, Grade: 0, VehicleMaxKMH: vmax})
	ascending := SpeedLimit(SpeedLimitInputs{CurveRadiusM: 0, Grade: 0.05, VehicleMaxKMH: vmax})
	if !almostEqual(ascending, level) {
		t.Errorf("ascending: got %v, want equal to level %v", ascending, level)
	}
}

func TestSpeedLimit_nonPositiveVehicleMaxReturnsZero(t *testing.T) {
	tests := []struct {
		name string
		vmax float64
	}{
		{"zero", 0},
		{"negative", -50},
		{"nan", math.NaN()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SpeedLimit(SpeedLimitInputs{
				CurveRadiusM:  400,
				AppliedCantMM: 100,
				Grade:         0,
				VehicleMaxKMH: tc.vmax,
			})
			if got != 0 {
				t.Errorf("vehicle max %v: got %v, want 0", tc.vmax, got)
			}
		})
	}
}

func TestSpeedLimit_negativeRadiusTreatedAsTangent(t *testing.T) {
	const vmax = 250.0
	got := SpeedLimit(SpeedLimitInputs{
		CurveRadiusM:  -500, // invalid; treat as tangent
		AppliedCantMM: 0,
		Grade:         0,
		VehicleMaxKMH: vmax,
	})
	if !almostEqual(got, vmax) {
		t.Errorf("negative radius: got %v, want tangent result %v", got, vmax)
	}
}

// TestSpeedLimit_badInputsGuarded covers the input-normalization guards: NaN or
// negative applied cant is treated as zero applied cant, and NaN grade is
// treated as level (no derate). None of these produce a non-finite result.
func TestSpeedLimit_badInputsGuarded(t *testing.T) {
	const (
		vmax   = 300.0
		radius = 400.0
	)

	zeroCant := SpeedLimit(SpeedLimitInputs{CurveRadiusM: radius, AppliedCantMM: 0, VehicleMaxKMH: vmax})

	t.Run("very negative applied cant normalized to zero", func(t *testing.T) {
		got := SpeedLimit(SpeedLimitInputs{CurveRadiusM: radius, AppliedCantMM: -10000, VehicleMaxKMH: vmax})
		if !almostEqual(got, zeroCant) {
			t.Errorf("negative applied cant: got %v, want equal to zero-cant %v", got, zeroCant)
		}
	})

	t.Run("NaN applied cant normalized to zero", func(t *testing.T) {
		got := SpeedLimit(SpeedLimitInputs{CurveRadiusM: radius, AppliedCantMM: math.NaN(), VehicleMaxKMH: vmax})
		if !almostEqual(got, zeroCant) {
			t.Errorf("NaN applied cant: got %v, want equal to zero-cant %v", got, zeroCant)
		}
	})

	t.Run("NaN grade treated as level", func(t *testing.T) {
		level := SpeedLimit(SpeedLimitInputs{CurveRadiusM: 0, Grade: 0, VehicleMaxKMH: vmax})
		got := SpeedLimit(SpeedLimitInputs{CurveRadiusM: 0, Grade: math.NaN(), VehicleMaxKMH: vmax})
		if !almostEqual(got, level) {
			t.Errorf("NaN grade: got %v, want equal to level %v", got, level)
		}
	})

	t.Run("NaN deficiency falls back to default", func(t *testing.T) {
		got := SpeedLimit(SpeedLimitInputs{CurveRadiusM: radius, AppliedCantMM: 0, MaxCantDeficiencyMM: math.NaN(), VehicleMaxKMH: vmax})
		if !almostEqual(got, zeroCant) {
			t.Errorf("NaN deficiency: got %v, want equal to default-deficiency %v", got, zeroCant)
		}
	})
}

func TestSpeedLimit_representativeCases(t *testing.T) {
	tests := []struct {
		name string
		in   SpeedLimitInputs
		want float64
	}{
		{
			name: "tangent level clamps to vehicle max",
			in:   SpeedLimitInputs{CurveRadiusM: 0, AppliedCantMM: 0, Grade: 0, VehicleMaxKMH: 320},
			want: 320,
		},
		{
			name: "large radius with cant clamps to vehicle max",
			in:   SpeedLimitInputs{CurveRadiusM: 7000, AppliedCantMM: 160, Grade: 0, VehicleMaxKMH: 320},
			want: 320,
		},
		{
			name: "moderate curve geometric limit",
			in:   SpeedLimitInputs{CurveRadiusM: 400, AppliedCantMM: 0, Grade: 0, VehicleMaxKMH: 1000},
			want: expectedGeometric(400, 0),
		},
		{
			name: "moderate curve with cant geometric limit",
			in:   SpeedLimitInputs{CurveRadiusM: 400, AppliedCantMM: 150, Grade: 0, VehicleMaxKMH: 1000},
			want: expectedGeometric(400, 150),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SpeedLimit(tc.in)
			if !almostEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// expectedGeometric independently recomputes the lateral (curve + cant) limit
// in km/h so the table tests assert against a value derived from the documented
// formula rather than the implementation under test. It uses the default cant
// deficiency, matching inputs that leave MaxCantDeficiencyMM unset.
func expectedGeometric(radiusM, cantMM float64) float64 {
	effectiveCantMM := cantMM + defaultMaxCantDeficiencyMM
	vMS := math.Sqrt((effectiveCantMM / gaugeMM) * gravityMS2 * radiusM)
	return vMS * msToKMH
}

func TestSpeedLimit_neverExceedsVehicleMaxAcrossGrid(t *testing.T) {
	radii := []float64{-100, 0, 50, 150, 400, 1200, 5000, 50000, math.Inf(1), math.NaN()}
	cants := []float64{-10000, 0, 50, 100, 150, 200, math.NaN()}
	deficiencies := []float64{0, 100, 150, 300, math.NaN()}
	grades := []float64{-0.9, -0.1, -0.05, -0.02, 0, 0.02, 0.1, math.NaN()}
	vmaxes := []float64{80, 160, 250, 320}

	for _, r := range radii {
		for _, c := range cants {
			for _, d := range deficiencies {
				for _, g := range grades {
					for _, v := range vmaxes {
						got := SpeedLimit(SpeedLimitInputs{
							CurveRadiusM:        r,
							AppliedCantMM:       c,
							MaxCantDeficiencyMM: d,
							Grade:               g,
							VehicleMaxKMH:       v,
						})
						if got > v+speedTol {
							t.Errorf("inputs r=%v c=%v d=%v g=%v v=%v: got %v, exceeds vehicle max", r, c, d, g, v, got)
						}
						if got < 0 {
							t.Errorf("inputs r=%v c=%v d=%v g=%v v=%v: got %v, must be non-negative", r, c, d, g, v, got)
						}
						if math.IsNaN(got) || math.IsInf(got, 0) {
							t.Errorf("inputs r=%v c=%v d=%v g=%v v=%v: got non-finite %v", r, c, d, g, v, got)
						}
					}
				}
			}
		}
	}
}
