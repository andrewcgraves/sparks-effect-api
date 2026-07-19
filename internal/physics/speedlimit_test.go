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

func TestSpeedLimit_gentleDescentNotDerated(t *testing.T) {
	const vmax = 200.0
	// A descent gentler than the threshold must not be derated.
	got := SpeedLimit(SpeedLimitInputs{CurveRadiusM: 0, Grade: -(gradeDerateThreshold), VehicleMaxKMH: vmax})
	if !almostEqual(got, vmax) {
		t.Errorf("gentle descent at threshold: got %v, want %v (no derate)", got, vmax)
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
// formula rather than the implementation under test.
func expectedGeometric(radiusM, cantMM float64) float64 {
	effectiveCantMM := cantMM + maxCantDeficiencyMM
	vMS := math.Sqrt((effectiveCantMM / gaugeMM) * gravityMS2 * radiusM)
	return vMS * msToKMH
}

func TestSpeedLimit_neverExceedsVehicleMaxAcrossGrid(t *testing.T) {
	radii := []float64{-100, 0, 50, 150, 400, 1200, 5000, 50000, math.Inf(1)}
	cants := []float64{0, 50, 100, 150, 200}
	grades := []float64{-0.9, -0.1, -0.05, -0.02, 0, 0.02, 0.1}
	vmaxes := []float64{80, 160, 250, 320}

	for _, r := range radii {
		for _, c := range cants {
			for _, g := range grades {
				for _, v := range vmaxes {
					got := SpeedLimit(SpeedLimitInputs{
						CurveRadiusM:  r,
						AppliedCantMM: c,
						Grade:         g,
						VehicleMaxKMH: v,
					})
					if got > v+speedTol {
						t.Errorf("inputs r=%v c=%v g=%v v=%v: got %v, exceeds vehicle max", r, c, g, v, got)
					}
					if got < 0 {
						t.Errorf("inputs r=%v c=%v g=%v v=%v: got %v, must be non-negative", r, c, g, v, got)
					}
					if math.IsNaN(got) || math.IsInf(got, 0) {
						t.Errorf("inputs r=%v c=%v g=%v v=%v: got non-finite %v", r, c, g, v, got)
					}
				}
			}
		}
	}
}
