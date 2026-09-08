package physics

import (
	"math"
	"testing"
)

const timeTol = 1e-4

func testVehicle() VehicleLimits {
	// Clean numbers: 36 km/h = 10 m/s exactly, so hand-worked kinematics stay
	// exact rather than needing float rounding in the comments below.
	return VehicleLimits{MaxSpeedKMH: 36, AccelerationMS2: 1, DecelerationMS2: 1}
}

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
	const want = 15.491933384829668
	if !almostEqualTol(got, want, timeTol) {
		t.Errorf("SpanRunSeconds() = %v, want %v (±%v)", got, want, timeTol)
	}
}

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
	const want = 39.33333333333333
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
