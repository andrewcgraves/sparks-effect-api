package transit

import (
	"encoding/csv"
	"math"
	"os"
	"strconv"
	"testing"
)

const businessPlanMatrixPath = "testdata/ca-hsr-2026bp-all-stop-minutes.csv"

var businessPlanStationSlugs = map[string]string{
	"San Francisco":   "sf",
	"Millbrae":        "millbrae",
	"San Jose":        "san-jose",
	"Gilroy":          "gilroy",
	"Merced":          "merced",
	"Madera":          "madera",
	"Fresno":          "fresno",
	"Kings/Tulare":    "kings-tulare",
	"Bakersfield":     "bakersfield",
	"Palmdale":        "palmdale",
	"Victorville":     "victor-valley",
	"Burbank Airport": "burbank-airport",
	"Los Angeles":     "los-angeles",
	"Anaheim":         "anaheim",
	"Las Vegas":       "las-vegas",
}

func TestSeededCAHSRMatchesBusinessPlanMatrix(t *testing.T) {
	// The matrix's Brightline cells include a connection at Palmdale that the
	// graph cannot represent (it charges nothing to change trains), so the
	// Brightline stations are not held to it. See segment_run_times.yaml.
	notFitted := map[string]bool{"victor-valley": true, "las-vegas": true}

	// Every pair, not just the adjacent ones: the defect this pins (SPA-364)
	// was a topology error that left each hop plausible and the long trips
	// ~35 min out.
	const toleranceMins = 6.0
	tight := map[[2]string]float64{
		{"san-jose", "fresno"}: 3,
		{"fresno", "san-jose"}: 3,
	}

	store := mustNewStore(t)
	matrix := readBusinessPlanMatrix(t)

	checked := 0
	for pair, wantMins := range matrix {
		from, to := pair[0], pair[1]
		if notFitted[from] || notFitted[to] {
			continue
		}
		secs, _, _, ok := store.TravelTimeBetween("ca-hsr", from, to)
		if !ok {
			t.Errorf("%s → %s: no path in the compiled graph", from, to)
			continue
		}
		checked++

		limit := toleranceMins
		if tol, ok := tight[pair]; ok {
			limit = tol
		}
		gotMins := float64(secs) / 60
		if diff := gotMins - wantMins; math.Abs(diff) > limit {
			t.Errorf("%s → %s: compiled %.1f min, Business Plan %.0f min (%+.1f, over the %.0f min tolerance)",
				from, to, gotMins, wantMins, diff, limit)
		}
	}

	// 13 stations, every ordered pair.
	if checked != 13*12 {
		t.Errorf("checked %d pairs, want %d — the matrix or the station mapping lost some", checked, 13*12)
	}
}

func readBusinessPlanMatrix(t *testing.T) map[[2]string]float64 {
	t.Helper()

	f, err := os.Open(businessPlanMatrixPath)
	if err != nil {
		t.Fatalf("open %s: %v", businessPlanMatrixPath, err)
	}
	defer func() { _ = f.Close() }()

	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read %s: %v", businessPlanMatrixPath, err)
	}
	if len(rows) < 2 {
		t.Fatalf("%s: want a header row and station rows, got %d rows", businessPlanMatrixPath, len(rows))
	}

	slugOf := func(name string) string {
		slug, ok := businessPlanStationSlugs[name]
		if !ok {
			t.Fatalf("%s: station %q has no slug mapping", businessPlanMatrixPath, name)
		}
		return slug
	}

	header := rows[0]
	out := make(map[[2]string]float64)
	for _, row := range rows[1:] {
		from := slugOf(row[0])
		for col := 1; col < len(row) && col < len(header); col++ {
			if row[col] == "" {
				continue
			}
			mins, err := strconv.ParseFloat(row[col], 64)
			if err != nil {
				t.Fatalf("%s: %s → %s: %v", businessPlanMatrixPath, row[0], header[col], err)
			}
			out[[2]string{from, slugOf(header[col])}] = mins
		}
	}
	return out
}
