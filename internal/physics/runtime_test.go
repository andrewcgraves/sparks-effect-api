package physics

import (
	"math"
	"testing"
)

// timeTol is the absolute tolerance (seconds) used when comparing computed
// run times against a hand-worked reference value.
const timeTol = 1e-4

func testVehicle() VehicleLimits {
	// Clean numbers: 36 km/h = 10 m/s exactly, so hand-worked kinematics stay
	// exact rather than needing float rounding in the comments below.
	return VehicleLimits{MaxSpeedKMH: 36, AccelerationMS2: 1, DecelerationMS2: 1}
}

// TestSpanRunSeconds_reachesCruise pins the trapezoidal (accelerate, cruise,
// decelerate) case against an independently hand-worked example.
//
// Worked example: vmax = 10 m/s, accel = decel = 1 m/s^2, single tangent
// segment of 300 m (cap = vehicle max, no lateral or grade limit).
//
//	accel distance to reach vmax: v^2/(2*accel) = 100/2 = 50 m, in 10 m/s / 1 = 10 s
//	decel distance from vmax:     same by symmetry              = 50 m, 10 s
//	cruise distance: 300 - 50 - 50 = 200 m, at 10 m/s            = 20 s
//	total: 10 + 10 + 20 = 40 s
func TestSpanRunSeconds_reachesCruise(t *testing.T) {
	span := InterStopSpan{
		FromStopID: "a",
		ToStopID:   "b",
		DistanceM:  300,
		Segments:   []SpanSegment{{DistanceM: 300, Physics: Segment{}}},
	}

	got, err := SpanRunSeconds(span, testVehicle())
	if err != nil {
		t.Fatalf("SpanRunSeconds() error = %v, want nil", err)
	}
	if !almostEqualTol(got, 40.0, timeTol) {
		t.Errorf("SpanRunSeconds() = %v, want 40.0 (±%v)", got, timeTol)
	}
}

// TestSpanRunSeconds_neverReachesCruise pins the triangular (accelerate then
// immediately decelerate, no cruise plateau) case against an independently
// hand-worked example.
//
// Worked example: vmax = 10 m/s, accel = decel = 1 m/s^2, single tangent
// segment of 60 m — too short to reach vmax (that needs 50+50=100 m, see
// TestSpanRunSeconds_reachesCruise).
//
//	peak^2 = (2*accel*decel*d + decel*entry^2 + accel*exit^2) / (accel+decel)
//	       = (2*1*1*60 + 0 + 0) / 2 = 60
//	peak   = sqrt(60) = 7.745966...  m/s (< vmax, confirms cruise is never reached)
//	time   = (peak-0)/accel + (peak-0)/decel = 2*sqrt(60) = 15.491933... s
//
// Independent check: distance accelerating 0->peak at 1 m/s^2 is
// peak^2/2 = 30 m; decelerating peak->0 is symmetric, another 30 m;
// 30+30 = 60 m, matching the segment length.
func TestSpanRunSeconds_neverReachesCruise(t *testing.T) {
	span := InterStopSpan{
		FromStopID: "a",
		ToStopID:   "b",
		DistanceM:  60,
		Segments:   []SpanSegment{{DistanceM: 60, Physics: Segment{}}},
	}

	got, err := SpanRunSeconds(span, testVehicle())
	if err != nil {
		t.Fatalf("SpanRunSeconds() error = %v, want nil", err)
	}
	const want = 15.491933384829668 // 2 * sqrt(60)
	if !almostEqualTol(got, want, timeTol) {
		t.Errorf("SpanRunSeconds() = %v, want %v (±%v)", got, want, timeTol)
	}
}

// TestSpanRunSeconds_midSpanCurveSlowsAndRecovers covers a multi-segment span
// whose middle segment has a materially lower pointwise cap than its
// neighbors (a curve dropping into the gap between two tangent stretches),
// checking two independent properties:
//
//  1. It takes longer than the same total distance run entirely at the
//     tangent cap — the curve must actually constrain the profile.
//  2. Its exact duration agrees with numericalRunSeconds, a brute-force
//     Euler/trapezoidal simulation that integrates the same physical model
//     (forward/backward reachability against a pointwise cap) by walking the
//     span in many small steps rather than the analytic closed-form
//     cruise/triangular formulas SpanRunSeconds uses — an independent method
//     that should converge to the same answer if the analytic integration is
//     correct.
func TestSpanRunSeconds_midSpanCurveSlowsAndRecovers(t *testing.T) {
	vehicle := testVehicle()
	tangent := Segment{}
	// CurveRadiusM chosen so the curve's cap (~4.43 m/s, computed from the
	// same cant-deficiency formula speedlimit_test.go pins) sits well below
	// both the 10 m/s vehicle max and what 200 m of accel from rest reaches.
	curve := Segment{CurveRadiusM: 20}

	span := InterStopSpan{
		FromStopID: "a",
		ToStopID:   "b",
		DistanceM:  430,
		Segments: []SpanSegment{
			{DistanceM: 200, Physics: tangent},
			{DistanceM: 30, Physics: curve},
			{DistanceM: 200, Physics: tangent},
		},
	}
	uncurvedSpan := InterStopSpan{
		FromStopID: "a",
		ToStopID:   "b",
		DistanceM:  430,
		Segments:   []SpanSegment{{DistanceM: 430, Physics: tangent}},
	}

	got, err := SpanRunSeconds(span, vehicle)
	if err != nil {
		t.Fatalf("SpanRunSeconds() error = %v, want nil", err)
	}

	uncurved, err := SpanRunSeconds(uncurvedSpan, vehicle)
	if err != nil {
		t.Fatalf("SpanRunSeconds(uncurved) error = %v, want nil", err)
	}
	if got <= uncurved {
		t.Errorf("with-curve time %v should exceed uncurved time %v", got, uncurved)
	}

	distsM := []float64{200, 30, 200}
	capsMS := []float64{
		kmhToMS(SpeedLimit(SpeedLimitInputs{VehicleMaxKMH: vehicle.MaxSpeedKMH})),
		kmhToMS(SpeedLimit(SpeedLimitInputs{CurveRadiusM: 20, VehicleMaxKMH: vehicle.MaxSpeedKMH})),
		kmhToMS(SpeedLimit(SpeedLimitInputs{VehicleMaxKMH: vehicle.MaxSpeedKMH})),
	}
	want := numericalRunSeconds(distsM, capsMS, vehicle.AccelerationMS2, vehicle.DecelerationMS2, 0.05)
	if !almostEqualTol(got, want, 0.1) {
		t.Errorf("SpanRunSeconds() = %v, want %v (±0.1s, numerical reference)", got, want)
	}
}

// TestSpanRunSeconds_descendingGradeIncreasesTime exercises the
// GradePct -> ratio conversion SpanRunSeconds does before calling SpeedLimit
// (Segment.GradePct is a percent; SpeedLimitInputs.Grade is a ratio) — a
// spot with no other coverage, since every other SpanRunSeconds test uses
// level track. A wrong or missing /100 would silently change the derate
// factor below and this golden value would no longer match.
//
// Worked example: vmax = 10 m/s, accel = decel = 1 m/s^2, a single 200 m
// segment with GradePct = -10 (a 10% descent, ratio 0.10).
//
//	speedlimit.go's descending-grade derate: d = 0.10, threshold = 0.02,
//	coeff = 5.0, so factor = 1 - 5*(0.10-0.02) = 1 - 0.4 = 0.6
//	cap = 10 * 0.6 = 6 m/s
//	accel/decel distance to cap: 6^2/2 = 18 m each (36 m total)
//	cruise: 200 - 36 = 164 m at 6 m/s = 27.333... s
//	total: 6 + 6 + 27.333... = 39.333... s
//
// A level (grade 0) span of the same 200 m instead cruises at the full
// 10 m/s cap (50+50 m accel/decel, 100 m cruise at 10 m/s = 10 s): 10+10+10 =
// 30 s — strictly less, since the descent must derate the cap.
func TestSpanRunSeconds_descendingGradeIncreasesTime(t *testing.T) {
	vehicle := testVehicle()
	graded := InterStopSpan{
		DistanceM: 200,
		Segments:  []SpanSegment{{DistanceM: 200, Physics: Segment{GradePct: -10}}},
	}
	level := InterStopSpan{
		DistanceM: 200,
		Segments:  []SpanSegment{{DistanceM: 200, Physics: Segment{}}},
	}

	got, err := SpanRunSeconds(graded, vehicle)
	if err != nil {
		t.Fatalf("SpanRunSeconds(graded) error = %v, want nil", err)
	}
	const want = 39.33333333333333 // 6 + 6 + 164.0/6.0
	if !almostEqualTol(got, want, timeTol) {
		t.Errorf("SpanRunSeconds(graded) = %v, want %v (±%v)", got, want, timeTol)
	}

	levelSecs, err := SpanRunSeconds(level, vehicle)
	if err != nil {
		t.Fatalf("SpanRunSeconds(level) error = %v, want nil", err)
	}
	if !almostEqualTol(levelSecs, 30.0, timeTol) {
		t.Errorf("SpanRunSeconds(level) = %v, want 30.0 (±%v)", levelSecs, timeTol)
	}
	if got <= levelSecs {
		t.Errorf("descending-grade time %v should exceed level time %v", got, levelSecs)
	}
}

func TestSpanRunSeconds_rejectsNonPositiveVehicleParams(t *testing.T) {
	validSpan := InterStopSpan{
		DistanceM: 100,
		Segments:  []SpanSegment{{DistanceM: 100, Physics: Segment{}}},
	}

	tests := []struct {
		name    string
		vehicle VehicleLimits
	}{
		{"zero max speed", VehicleLimits{MaxSpeedKMH: 0, AccelerationMS2: 1, DecelerationMS2: 1}},
		{"negative max speed", VehicleLimits{MaxSpeedKMH: -10, AccelerationMS2: 1, DecelerationMS2: 1}},
		{"zero acceleration", VehicleLimits{MaxSpeedKMH: 36, AccelerationMS2: 0, DecelerationMS2: 1}},
		{"negative acceleration", VehicleLimits{MaxSpeedKMH: 36, AccelerationMS2: -1, DecelerationMS2: 1}},
		{"zero deceleration", VehicleLimits{MaxSpeedKMH: 36, AccelerationMS2: 1, DecelerationMS2: 0}},
		{"negative deceleration", VehicleLimits{MaxSpeedKMH: 36, AccelerationMS2: 1, DecelerationMS2: -1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := SpanRunSeconds(validSpan, tc.vehicle); err == nil {
				t.Errorf("SpanRunSeconds() error = nil, want an error for %s", tc.name)
			}
		})
	}
}

func TestSpanRunSeconds_errorsOnPositiveDistanceWithNoSegments(t *testing.T) {
	span := InterStopSpan{DistanceM: 100, Segments: nil}
	if _, err := SpanRunSeconds(span, testVehicle()); err == nil {
		t.Error("SpanRunSeconds() error = nil, want an error for a span with distance but no segments")
	}
}

func TestSpanRunSeconds_zeroDistanceSpanIsInstantaneous(t *testing.T) {
	span := InterStopSpan{DistanceM: 0, Segments: nil}
	got, err := SpanRunSeconds(span, testVehicle())
	if err != nil {
		t.Fatalf("SpanRunSeconds() error = %v, want nil", err)
	}
	if got != 0 {
		t.Errorf("SpanRunSeconds() = %v, want 0", got)
	}
}

// numericalRunSeconds is an independent brute-force reference for
// SpanRunSeconds: it subdivides each macro segment into many fine steps of
// at most stepM, runs the same forward/backward reachability passes at that
// fine resolution, and integrates time as distance / average-of-endpoint-speeds
// per fine step, rather than using the analytic cruise/triangular formulas
// under test.
func numericalRunSeconds(distsM, capsMS []float64, accel, decel, stepM float64) float64 {
	var fineDists, fineCaps []float64
	for i, d := range distsM {
		steps := int(math.Ceil(d / stepM))
		stepLen := d / float64(steps)
		for s := 0; s < steps; s++ {
			fineDists = append(fineDists, stepLen)
			fineCaps = append(fineCaps, capsMS[i])
		}
	}

	n := len(fineDists)
	fwd := make([]float64, n+1)
	for i := 1; i <= n; i++ {
		fwd[i] = math.Min(fineCaps[i-1], math.Sqrt(fwd[i-1]*fwd[i-1]+2*accel*fineDists[i-1]))
	}
	bwd := make([]float64, n+1)
	for i := n - 1; i >= 0; i-- {
		bwd[i] = math.Min(fineCaps[i], math.Sqrt(bwd[i+1]*bwd[i+1]+2*decel*fineDists[i]))
	}

	var totalSecs float64
	for i := 0; i < n; i++ {
		v0 := math.Min(fwd[i], bwd[i])
		v1 := math.Min(fwd[i+1], bwd[i+1])
		avg := (v0 + v1) / 2
		if avg <= 0 {
			continue
		}
		totalSecs += fineDists[i] / avg
	}
	return totalSecs
}
