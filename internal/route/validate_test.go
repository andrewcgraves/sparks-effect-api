package route

import (
	"math"
	"strings"
	"testing"
)

// line builds a valid n-point LineString with matching zero-valued segments.
func line(n int) Ingest {
	coords := make([][]float64, n)
	for i := range coords {
		coords[i] = []float64{-122.0 + float64(i)*0.01, 37.0}
	}
	return Ingest{
		Type:        "LineString",
		Coordinates: coords,
		Properties: Properties{
			Name:     "Test Route",
			Mode:     "rail",
			Segments: make([]Segment, n-1),
		},
	}
}

func TestValidateAcceptsWellFormedRoute(t *testing.T) {
	in := line(3)
	in.Properties.Segments = []Segment{
		{CantMM: 150, CurveRadiusM: 1200, GradePct: 1.2},
		{CantMM: 0, CurveRadiusM: 0, GradePct: -0.8},
	}
	if err := Validate(in); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

// Segments are optional: omitting them means tangent, level, uncanted track.
func TestValidateAcceptsOmittedSegments(t *testing.T) {
	in := line(3)
	in.Properties.Segments = nil
	if err := Validate(in); err != nil {
		t.Fatalf("Validate() with no segments = %v, want nil", err)
	}
}

func TestValidateRejectsBadGeometry(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*Ingest)
		want string
	}{
		{"wrong type", func(in *Ingest) { in.Type = "Point" }, "LineString"},
		{"empty type", func(in *Ingest) { in.Type = "" }, "LineString"},
		{"single point", func(in *Ingest) {
			in.Coordinates = in.Coordinates[:1]
			in.Properties.Segments = nil
		}, "at least 2"},
		{"no coordinates", func(in *Ingest) {
			in.Coordinates = nil
			in.Properties.Segments = nil
		}, "at least 2"},
		{"short position", func(in *Ingest) { in.Coordinates[1] = []float64{-122.0} }, "[longitude, latitude]"},
		{"long position", func(in *Ingest) { in.Coordinates[1] = []float64{-122.0, 37.0, 5.0} }, "[longitude, latitude]"},
		{"longitude too low", func(in *Ingest) { in.Coordinates[1][0] = -180.5 }, "longitude"},
		{"longitude too high", func(in *Ingest) { in.Coordinates[1][0] = 180.5 }, "longitude"},
		{"latitude too low", func(in *Ingest) { in.Coordinates[1][1] = -90.5 }, "latitude"},
		{"latitude too high", func(in *Ingest) { in.Coordinates[1][1] = 90.5 }, "latitude"},
		{"non-finite longitude", func(in *Ingest) { in.Coordinates[1][0] = math.NaN() }, "longitude"},
		{"infinite latitude", func(in *Ingest) { in.Coordinates[1][1] = math.Inf(1) }, "latitude"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := line(3)
			tc.mut(&in)
			err := Validate(in)
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate() = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// The segment list describes the gaps between points, so a route of n points
// has exactly n-1 segments. A mismatch means the physics do not line up with
// the geometry they describe.
func TestValidateRejectsSegmentCountMismatch(t *testing.T) {
	for _, n := range []int{1, 3} {
		in := line(3)
		in.Properties.Segments = make([]Segment, n)
		err := Validate(in)
		if err == nil {
			t.Fatalf("Validate() with %d segments for 3 points = nil, want error", n)
		}
		if !strings.Contains(err.Error(), "segments") {
			t.Errorf("Validate() = %q, want it to mention segments", err)
		}
	}
}

func TestValidateRejectsOutOfRangePhysics(t *testing.T) {
	tests := []struct {
		name string
		seg  Segment
		want string
	}{
		{"negative cant", Segment{CantMM: -1}, "cant_mm"},
		{"cant above ceiling", Segment{CantMM: MaxCantMM + 1}, "cant_mm"},
		{"non-finite cant", Segment{CantMM: math.NaN()}, "cant_mm"},
		{"negative radius", Segment{CurveRadiusM: -1}, "curve_radius_m"},
		{"radius below minimum", Segment{CurveRadiusM: MinCurveRadiusM - 1}, "curve_radius_m"},
		{"radius above maximum", Segment{CurveRadiusM: MaxCurveRadiusM + 1}, "curve_radius_m"},
		{"non-finite radius", Segment{CurveRadiusM: math.Inf(1)}, "curve_radius_m"},
		{"grade too steep down", Segment{GradePct: -MaxGradePct - 0.1}, "grade_pct"},
		{"grade too steep up", Segment{GradePct: MaxGradePct + 0.1}, "grade_pct"},
		{"non-finite grade", Segment{GradePct: math.NaN()}, "grade_pct"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := line(2)
			in.Properties.Segments = []Segment{tc.seg}
			err := Validate(in)
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate() = %q, want it to contain %q", err, tc.want)
			}
			// The message must say which segment is at fault, not just that
			// one of them is.
			if !strings.Contains(err.Error(), "segment 0") {
				t.Errorf("Validate() = %q, want it to identify the offending segment", err)
			}
		})
	}
}

// Radius 0 is the sentinel for tangent (straight) track, so it must pass even
// though it sits below MinCurveRadiusM.
func TestValidateAcceptsTangentTrackSentinel(t *testing.T) {
	in := line(2)
	in.Properties.Segments = []Segment{{CurveRadiusM: 0}}
	if err := Validate(in); err != nil {
		t.Fatalf("Validate() with radius 0 = %v, want nil", err)
	}
}

func TestValidateAcceptsPhysicsRangeBoundaries(t *testing.T) {
	in := line(2)
	in.Properties.Segments = []Segment{
		{CantMM: MaxCantMM, CurveRadiusM: MinCurveRadiusM, GradePct: MaxGradePct},
	}
	if err := Validate(in); err != nil {
		t.Fatalf("Validate() at range boundaries = %v, want nil", err)
	}
}

func TestValidateRequiresName(t *testing.T) {
	in := line(2)
	in.Properties.Name = "   "
	err := Validate(in)
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("Validate() = %v, want a name error", err)
	}
}

func TestValidateRejectsUnknownMode(t *testing.T) {
	in := line(2)
	in.Properties.Mode = "teleport"
	err := Validate(in)
	if err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("Validate() = %v, want a mode error", err)
	}
}

func TestValidateRejectsMalformedExplicitSlug(t *testing.T) {
	in := line(2)
	in.Properties.Slug = "Not A Slug!"
	err := Validate(in)
	if err == nil || !strings.Contains(err.Error(), "slug") {
		t.Fatalf("Validate() = %v, want a slug error", err)
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"CA HSR Phase 1", "ca-hsr-phase-1"},
		{"  Leading and trailing  ", "leading-and-trailing"},
		{"San Francisco — Anaheim", "san-francisco-anaheim"},
		{"Multiple   spaces", "multiple-spaces"},
		{"already-a-slug", "already-a-slug"},
		{"Punctuation!@#$%", "punctuation"},
		{"--dashes--", "dashes"},
		{"", ""},
		{"!!!", ""},
	}
	for _, tc := range tests {
		if got := Slugify(tc.in); got != tc.want {
			t.Errorf("Slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Every slug Slugify produces must itself be accepted as an explicit slug,
// otherwise a name the caller can post would derive a slug the API rejects.
func TestSlugifyOutputIsAValidSlug(t *testing.T) {
	for _, name := range []string{"CA HSR Phase 1", "San Francisco — Anaheim", "already-a-slug"} {
		s := Slugify(name)
		if !IsValidSlug(s) {
			t.Errorf("Slugify(%q) = %q, which IsValidSlug rejects", name, s)
		}
	}
}
