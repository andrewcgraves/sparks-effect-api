package physics

import (
	"fmt"
	"math"
)

type VehicleLimits struct {
	MaxSpeedKMH     float64
	AccelerationMS2 float64
	DecelerationMS2 float64
}

func SpanRunSeconds(span InterStopSpan, vehicle VehicleLimits) (float64, error) {
	if !(vehicle.MaxSpeedKMH > 0) {
		return 0, fmt.Errorf("vehicle max speed must be > 0, got %v", vehicle.MaxSpeedKMH)
	}
	if !(vehicle.AccelerationMS2 > 0) {
		return 0, fmt.Errorf("vehicle acceleration must be > 0, got %v", vehicle.AccelerationMS2)
	}
	if !(vehicle.DecelerationMS2 > 0) {
		return 0, fmt.Errorf("vehicle deceleration must be > 0, got %v", vehicle.DecelerationMS2)
	}
	if span.DistanceM <= 0 {
		return 0, nil
	}
	if len(span.Segments) == 0 {
		return 0, fmt.Errorf("span has positive distance %v but no segments", span.DistanceM)
	}

	n := len(span.Segments)
	capsMS := make([]float64, n)
	distsM := make([]float64, n)
	for i, seg := range span.Segments {
		capsMS[i] = kmhToMS(SpeedLimit(SpeedLimitInputs{
			CurveRadiusM:  seg.Physics.CurveRadiusM,
			AppliedCantMM: seg.Physics.CantMM,
			Grade:         seg.Physics.GradePct / 100,
			VehicleMaxKMH: vehicle.MaxSpeedKMH,
		}))
		// A non-positive or non-finite cap would divide by zero (or worse)
		// in segmentSeconds' cruise-time term below. SpeedLimit cannot
		// produce one given the VehicleMaxKMH > 0 already checked above, but
		// this keeps that invariant explicit rather than relying on a caller
		// three functions away.
		if !(capsMS[i] > 0) || math.IsInf(capsMS[i], 0) {
			return 0, fmt.Errorf("segment %d has a non-positive or infinite speed cap (%v m/s)", i, capsMS[i])
		}
		distsM[i] = seg.DistanceM
	}

	nodeSpeeds := boundedNodeSpeeds(distsM, capsMS, vehicle.AccelerationMS2, vehicle.DecelerationMS2)

	var totalSecs float64
	for i := 0; i < n; i++ {
		totalSecs += segmentSeconds(nodeSpeeds[i], nodeSpeeds[i+1], distsM[i], capsMS[i],
			vehicle.AccelerationMS2, vehicle.DecelerationMS2)
	}
	return totalSecs, nil
}

func kmhToMS(kmh float64) float64 { return kmh / msToKMH }

func boundedNodeSpeeds(distsM, capsMS []float64, accel, decel float64) []float64 {
	n := len(distsM)

	fwd := make([]float64, n+1)
	for i := 1; i <= n; i++ {
		reachable := math.Sqrt(fwd[i-1]*fwd[i-1] + 2*accel*distsM[i-1])
		fwd[i] = math.Min(capsMS[i-1], reachable)
	}

	bwd := make([]float64, n+1)
	for i := n - 1; i >= 0; i-- {
		reachable := math.Sqrt(bwd[i+1]*bwd[i+1] + 2*decel*distsM[i])
		bwd[i] = math.Min(capsMS[i], reachable)
	}

	out := make([]float64, n+1)
	for i := range out {
		out[i] = math.Min(fwd[i], bwd[i])
	}
	return out
}

func segmentSeconds(entryMS, exitMS, distM, capMS, accel, decel float64) float64 {
	if distM <= 0 {
		return 0
	}

	accelDist := (capMS*capMS - entryMS*entryMS) / (2 * accel)
	decelDist := (capMS*capMS - exitMS*exitMS) / (2 * decel)
	if accelDist+decelDist <= distM {
		cruiseDist := distM - accelDist - decelDist
		accelSecs := (capMS - entryMS) / accel
		decelSecs := (capMS - exitMS) / decel
		cruiseSecs := cruiseDist / capMS
		return accelSecs + decelSecs + cruiseSecs
	}

	// Triangular profile: solve for the peak speed reached partway through
	// the segment. Distance covered accelerating from entryMS to the peak
	// plus distance covered decelerating from the peak to exitMS must equal
	// distM:
	//   (peak^2-entryMS^2)/(2*accel) + (peak^2-exitMS^2)/(2*decel) = distM
	// which rearranges to the peakSq expression below. boundedNodeSpeeds
	// guarantees entryMS and exitMS are themselves reachable within distM at
	// these accel/decel rates, so peak is always >= both; the max() clamp
	// only guards floating-point rounding at the branch boundary.
	peakSq := (2*accel*decel*distM + decel*entryMS*entryMS + accel*exitMS*exitMS) / (accel + decel)
	peak := math.Sqrt(math.Max(peakSq, math.Max(entryMS*entryMS, exitMS*exitMS)))
	return (peak-entryMS)/accel + (peak-exitMS)/decel
}
