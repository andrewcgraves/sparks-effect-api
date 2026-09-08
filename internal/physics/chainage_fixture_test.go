package physics_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/physics"
)

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
