package route

import (
	"fmt"
	"strings"
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
	if in.Type != "LineString" {
		return fmt.Errorf("geometry type must be %q, got %q", "LineString", in.Type)
	}
	if len(in.Coordinates) < MinCoordinates {
		return fmt.Errorf("a route needs at least %d coordinates, got %d", MinCoordinates, len(in.Coordinates))
	}
	for i, pos := range in.Coordinates {
		if err := validatePosition(pos); err != nil {
			return fmt.Errorf("coordinate %d: %w", i, err)
		}
		// A repeated point is a zero-length span. It is almost always an
		// authoring slip, and it is not harmless: everything downstream that
		// divides by segment length — chainage, projection, run-profile
		// integration — would divide by zero.
		if i > 0 && samePosition(in.Coordinates[i-1], pos) {
			return fmt.Errorf("coordinate %d repeats coordinate %d, giving a zero-length segment", i, i-1)
		}
	}

	if strings.TrimSpace(in.Properties.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if in.Properties.Mode != "" && !validModes[in.Properties.Mode] {
		return fmt.Errorf("unknown mode %q", in.Properties.Mode)
	}
	if in.Properties.Slug != "" && !IsValidSlug(in.Properties.Slug) {
		return fmt.Errorf("slug %q must be lowercase alphanumeric words separated by single hyphens", in.Properties.Slug)
	}

	// Segments describe the gaps between points, so n points have n-1 spans.
	// An omitted list is fine — it means every span is tangent and level — but
	// a list of the wrong length means the physics do not line up with the
	// geometry they are meant to describe.
	if segs := in.Properties.Segments; len(segs) > 0 {
		if want := len(in.Coordinates) - 1; len(segs) != want {
			return fmt.Errorf("expected %d segments for %d coordinates, got %d", want, len(in.Coordinates), len(segs))
		}
		for i, seg := range segs {
			if err := validateSegment(seg); err != nil {
				return fmt.Errorf("segment %d: %w", i, err)
			}
		}
	}

	return nil
}

func validatePosition(pos []float64) error {
	if len(pos) != 2 {
		return fmt.Errorf("must be [longitude, latitude], got %d values", len(pos))
	}
	if lng := pos[0]; !inRange(lng, -180, 180) {
		return fmt.Errorf("longitude %v is outside [-180, 180]", lng)
	}
	if lat := pos[1]; !inRange(lat, -90, 90) {
		return fmt.Errorf("latitude %v is outside [-90, 90]", lat)
	}
	return nil
}

func validateSegment(seg Segment) error {
	if !inRange(seg.CantMM, 0, MaxCantMM) {
		return fmt.Errorf("cant_mm %v is outside [0, %g]", seg.CantMM, MaxCantMM)
	}
	// Radius 0 is the sentinel for tangent (straight) track, so it is accepted
	// even though it sits below the minimum real curve radius.
	if seg.CurveRadiusM != 0 && !inRange(seg.CurveRadiusM, MinCurveRadiusM, MaxCurveRadiusM) {
		return fmt.Errorf("curve_radius_m %v is outside [%g, %g] (use 0 for tangent track)",
			seg.CurveRadiusM, MinCurveRadiusM, MaxCurveRadiusM)
	}
	if !inRange(seg.GradePct, -MaxGradePct, MaxGradePct) {
		return fmt.Errorf("grade_pct %v is outside [%g, %g]", seg.GradePct, -MaxGradePct, MaxGradePct)
	}
	return nil
}

func inRange(v, lo, hi float64) bool {
	return v >= lo && v <= hi
}

func samePosition(a, b []float64) bool {
	return a[0] == b[0] && a[1] == b[1]
}
