package route

import (
	"fmt"
	"strings"

	"github.com/andrewcgraves/sparks-effect-api/internal/fault"
)

const (
	MaxCantMM       = 300.0
	MinCurveRadiusM = 20.0
	MaxCurveRadiusM = 100000.0
	MaxGradePct     = 15.0
	MinCoordinates  = 2
)

var validModes = map[string]bool{
	"rail":      true,
	"metro":     true,
	"tram":      true,
	"bus":       true,
	"ferry":     true,
	"funicular": true,
}

type Segment struct {
	CantMM       float64 `json:"cant_mm"`
	CurveRadiusM float64 `json:"curve_radius_m"`
	GradePct     float64 `json:"grade_pct"`
}

type Properties struct {
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Slug          string    `json:"slug"`
	Mode          string    `json:"mode"`
	Bidirectional *bool     `json:"bidirectional"`
	ScenarioSlug  string    `json:"scenario_slug"`
	Segments      []Segment `json:"segments"`
}

type Ingest struct {
	Type        string      `json:"type"`
	Coordinates [][]float64 `json:"coordinates"`
	Properties  Properties  `json:"properties"`
	BBox        []float64   `json:"bbox,omitempty"`
}

func Validate(in Ingest) error {
	var faults fault.ValidationFaults
	if in.Type != "LineString" {
		faults = append(faults, fault.Whole("type", fault.RuleType,
			fmt.Sprintf("geometry type must be %q, got %q", "LineString", in.Type)))
	}
	if len(in.Coordinates) < MinCoordinates {
		faults = append(faults, fault.Whole("coordinates", fault.RuleMinCount,
			fmt.Sprintf("a route needs at least %d coordinates, got %d", MinCoordinates, len(in.Coordinates))))
	}
	for i, pos := range in.Coordinates {
		faults = append(faults, positionFaults(i, pos)...)
		// A repeated point is a zero-length span. It is almost always an
		// authoring slip, and it is not harmless: everything downstream that
		// divides by segment length — chainage, projection, run-profile
		// integration — would divide by zero.
		if i > 0 && len(pos) >= 2 && len(in.Coordinates[i-1]) >= 2 && samePosition(in.Coordinates[i-1], pos) {
			faults = append(faults, fault.At("coordinates", i, fault.RuleZeroLength,
				fmt.Sprintf("coordinate %d repeats coordinate %d, giving a zero-length segment", i, i-1)))
		}
	}

	if strings.TrimSpace(in.Properties.Name) == "" {
		faults = append(faults, fault.Whole("name", fault.RuleRequired, "name is required"))
	}
	if in.Properties.Mode != "" && !validModes[in.Properties.Mode] {
		faults = append(faults, fault.Whole("mode", fault.RuleUnknown,
			fmt.Sprintf("unknown mode %q", in.Properties.Mode)))
	}
	if in.Properties.Slug != "" && !IsValidSlug(in.Properties.Slug) {
		faults = append(faults, fault.Whole("slug", fault.RuleFormat,
			fmt.Sprintf("slug %q must be lowercase alphanumeric words separated by single hyphens", in.Properties.Slug)))
	}

	// Segments describe the gaps between points, so n points have n-1 spans.
	// An omitted list is fine — it means every span is tangent and level — but
	// a list of the wrong length means the physics do not line up with the
	// geometry they are meant to describe.
	if segs := in.Properties.Segments; len(segs) > 0 {
		if want := len(in.Coordinates) - 1; len(segs) != want {
			faults = append(faults, fault.Whole("segments", fault.RuleCount,
				fmt.Sprintf("expected %d segments for %d coordinates, got %d", want, len(in.Coordinates), len(segs))))
		}
		for i, seg := range segs {
			faults = append(faults, segmentFaults(i, seg)...)
		}
	}

	return faults.Err()
}

func positionFaults(i int, pos []float64) fault.ValidationFaults {
	if len(pos) != 2 {
		return fault.ValidationFaults{fault.At("coordinates", i, fault.RuleCount,
			fmt.Sprintf("coordinate %d: must be [longitude, latitude], got %d values", i, len(pos)))}
	}
	var faults fault.ValidationFaults
	if lng := pos[0]; !inRange(lng, -180, 180) {
		faults = append(faults, fault.At("coordinates.lng", i, fault.RuleRange,
			fmt.Sprintf("coordinate %d: longitude %v is outside [-180, 180]", i, lng)))
	}
	if lat := pos[1]; !inRange(lat, -90, 90) {
		faults = append(faults, fault.At("coordinates.lat", i, fault.RuleRange,
			fmt.Sprintf("coordinate %d: latitude %v is outside [-90, 90]", i, lat)))
	}
	return faults
}

func segmentFaults(i int, seg Segment) fault.ValidationFaults {
	var faults fault.ValidationFaults
	if !inRange(seg.CantMM, 0, MaxCantMM) {
		faults = append(faults, fault.At("segments.cant_mm", i, fault.RuleRange,
			fmt.Sprintf("segment %d: cant_mm %v is outside [0, %g]", i, seg.CantMM, MaxCantMM)))
	}
	// Radius 0 is the sentinel for tangent (straight) track, so it is accepted
	// even though it sits below the minimum real curve radius.
	if seg.CurveRadiusM != 0 && !inRange(seg.CurveRadiusM, MinCurveRadiusM, MaxCurveRadiusM) {
		faults = append(faults, fault.At("segments.curve_radius_m", i, fault.RuleRange,
			fmt.Sprintf("segment %d: curve_radius_m %v is outside [%g, %g] (use 0 for tangent track)",
				i, seg.CurveRadiusM, MinCurveRadiusM, MaxCurveRadiusM)))
	}
	if !inRange(seg.GradePct, -MaxGradePct, MaxGradePct) {
		faults = append(faults, fault.At("segments.grade_pct", i, fault.RuleRange,
			fmt.Sprintf("segment %d: grade_pct %v is outside [%g, %g]", i, seg.GradePct, -MaxGradePct, MaxGradePct)))
	}
	return faults
}

func inRange(v, lo, hi float64) bool {
	return v >= lo && v <= hi
}

func samePosition(a, b []float64) bool {
	return a[0] == b[0] && a[1] == b[1]
}
