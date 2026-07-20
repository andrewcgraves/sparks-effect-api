// Package route validates admin-ingested route alignments: a GeoJSON
// LineString whose per-segment track physics are authored into its properties.
//
// Everything here is pure — value in, error out, no I/O and no state — so the
// ingestion rules can be exercised without a database or an HTTP server. The
// handler owns transport concerns (auth, status codes, persistence); this
// package owns "is this a coherent route?".
package route

import (
	"fmt"
	"strings"
)

// Physics ranges accepted for authored track geometry. These are sanity bounds
// meant to catch unit mistakes and typos (a cant in centimeters, a radius in
// kilometers), not to encode any particular design standard — the speed-limit
// model in internal/physics is what actually interprets these values.
const (
	// MaxCantMM is the ceiling on applied superelevation, in millimeters.
	// Real-world applied cant tops out around 180 mm; 300 mm leaves generous
	// headroom while still rejecting a value entered in the wrong unit.
	MaxCantMM = 300.0

	// MinCurveRadiusM and MaxCurveRadiusM bound real curvature, in meters.
	// The minimum is around the tightest street-running tram curve; the
	// maximum is the point past which a curve is straight for any practical
	// purpose and should be expressed as tangent track instead.
	//
	// A radius of exactly 0 is the tangent-track sentinel and is always
	// accepted — see validateSegment.
	MinCurveRadiusM = 20.0
	MaxCurveRadiusM = 100000.0

	// MaxGradePct bounds grade magnitude, in percent, in both directions.
	// 15% is far beyond adhesion rail (rack railways aside) and exists to
	// reject a grade supplied as a ratio scaled wrongly.
	MaxGradePct = 15.0

	// MinCoordinates is the fewest positions that describe a line.
	MinCoordinates = 2
)

// validModes are the transport modes a route may declare. An empty mode is
// also accepted and left for the caller to default.
var validModes = map[string]bool{
	"rail":      true,
	"metro":     true,
	"tram":      true,
	"bus":       true,
	"ferry":     true,
	"funicular": true,
}

// Segment is the authored track physics for one span between two consecutive
// coordinates. The zero value means tangent, level, uncanted track, which is
// why an omitted segment list is legitimate.
type Segment struct {
	CantMM       float64 `json:"cant_mm"`
	CurveRadiusM float64 `json:"curve_radius_m"`
	GradePct     float64 `json:"grade_pct"`
}

// Properties carries the non-geometric half of the ingestion payload.
type Properties struct {
	Name string `json:"name"`
	// Slug is optional; when empty the handler derives one from Name via
	// Slugify. When present it must already be in slug form.
	Slug string `json:"slug"`
	Mode string `json:"mode"`
	// Bidirectional is a pointer so an omitted field is distinguishable from
	// an explicit false, letting the caller default it to true.
	Bidirectional *bool `json:"bidirectional"`
	// ScenarioSlug optionally attaches the route to an existing scenario. An
	// ingested route is standalone by default — it belongs to no scenario and
	// carries no stops.
	ScenarioSlug string `json:"scenario_slug"`
	// Segments, when present, must have exactly one entry per span between
	// consecutive coordinates.
	Segments []Segment `json:"segments"`
}

// Ingest is the admin route-ingestion payload: a GeoJSON LineString geometry
// with route metadata and per-segment physics in its properties.
type Ingest struct {
	Type        string      `json:"type"`
	Coordinates [][]float64 `json:"coordinates"`
	Properties  Properties  `json:"properties"`
	// BBox is accepted and ignored. GeoJSON permits it on any geometry, and
	// the handler rejects unknown fields, so it is declared here purely so a
	// standards-conformant export is not turned away.
	BBox []float64 `json:"bbox,omitempty"`
}

// Validate reports the first problem with an ingestion payload, or nil if the
// route is coherent. Errors are phrased for the client: they name the offending
// field and, for physics, the index of the segment at fault.
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

// validatePosition checks a single GeoJSON position. Altitude is not modeled —
// grade is authored per segment instead — so a position is exactly two numbers.
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

// inRange reports whether v is finite and within [lo, hi]. Non-finite values
// need no separate check: NaN fails both comparisons, and each infinity fails
// one of them.
func inRange(v, lo, hi float64) bool {
	return v >= lo && v <= hi
}

// samePosition reports whether two validated positions are identical. Exact
// equality is the right test here: these are authored values that round-trip
// through JSON unchanged, not the result of arithmetic, so a tolerance would
// only start rejecting legitimately close points.
func samePosition(a, b []float64) bool {
	return a[0] == b[0] && a[1] == b[1]
}
