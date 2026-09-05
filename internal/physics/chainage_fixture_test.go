package physics_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/physics"
)

// chainageFixture is the shared statement of what "chainage" means, checked in
// as data rather than expressed only in Go.
//
// SPA-264 puts a second implementation of this arithmetic in the website, which
// slices a route alignment to a fraction handed to it by a chain result. That
// front end has no access to this package, so the only thing that can stop the
// two drifting is a fixture both assert against: a synthetic alignment and the
// chainage each of its vertices is at. Changing earthRadiusM, the projection,
// or the reference latitude turns this red here and turns its mirror red there,
// which is the point — a silent disagreement would move a drawn stub along the
// line with nothing to notice it.
//
// The alignment is deliberately synthetic and small enough to read: four
// vertices, some due north-south and some due east-west, at a latitude where
// the cosine correction is nowhere near 1.
type chainageFixture struct {
	Line            [][2]float64 `json:"line"`
	VertexChainageM []float64    `json:"vertex_chainage_m"`
}

func loadChainageFixture(t *testing.T) chainageFixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "chainage.golden.json"))
	if err != nil {
		t.Fatalf("reading chainage fixture: %v", err)
	}
	var f chainageFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("decoding chainage fixture: %v", err)
	}
	return f
}

func (f chainageFixture) points() []physics.Point {
	pts := make([]physics.Point, len(f.Line))
	for i, c := range f.Line {
		pts[i] = physics.Point{Lng: c[0], Lat: c[1]}
	}
	return pts
}

// Each vertex snaps to itself, so its chainage is the fixture's claim about the
// distance along the line to that vertex. Asserting through SnapStops rather
// than an unexported walk keeps this on the package's public seam.
func TestChainageFixture_vertexChainagesMatch(t *testing.T) {
	f := loadChainageFixture(t)
	line := f.points()
	if len(line) != len(f.VertexChainageM) {
		t.Fatalf("fixture has %d vertices but %d chainages", len(line), len(f.VertexChainageM))
	}

	stops := make([]physics.Stop, len(line))
	for i, p := range line {
		stops[i] = physics.Stop{ID: string(rune('a' + i)), Location: p}
	}

	got, err := physics.SnapStops(line, stops)
	if err != nil {
		t.Fatalf("SnapStops: %v", err)
	}
	// A tenth of a millimetre: far tighter than any rendering difference could
	// be, and loose enough that the fixture can carry human-readable numbers.
	const tolM = 1e-4
	for i, s := range got {
		if math.Abs(s.ChainageM-f.VertexChainageM[i]) > tolM {
			t.Errorf("vertex %d chainage = %.6f, fixture says %.6f", i, s.ChainageM, f.VertexChainageM[i])
		}
		if s.OffsetM > tolM {
			t.Errorf("vertex %d offset = %.6f, want 0 (a vertex snaps to itself)", i, s.OffsetM)
		}
	}
}
