package handler_test

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// snapResponse mirrors the endpoint's JSON so the tests assert against the wire
// shape a client actually sees, not the handler's internal types.
type snapResponse struct {
	RouteSlug          string  `json:"route_slug"`
	OffRouteThresholdM float64 `json:"off_route_threshold_m"`
	ChainageOrder      []int   `json:"chainage_order"`
	OrderMatchesInput  bool    `json:"order_matches_input"`
	Stops              []struct {
		Index     int     `json:"index"`
		ID        string  `json:"id"`
		Input     coord   `json:"input"`
		Snapped   coord   `json:"snapped"`
		ChainageM float64 `json:"chainage_m"`
		OffsetM   float64 `json:"offset_m"`
		OffRoute  bool    `json:"off_route"`
	} `json:"stops"`
}

type coord struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// seedSnapRoute ingests validRoute — the three-point alignment the route tests
// share — and returns the store it lives in. Its first vertex is
// (-122.4, 37.79) and it runs south-east from there.
func seedSnapRoute(t *testing.T) *fakeRouteStore {
	t.Helper()
	store := newFakeRouteStore()
	if rec := postJSON(t, handler.CreateRoute(store), "/api/admin/routes", validRoute); rec.Code != http.StatusCreated {
		t.Fatalf("seed create: status %d; body %s", rec.Code, rec.Body.String())
	}
	return store
}

func postSnapStops(t *testing.T, h http.Handler, slug, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/routes/"+slug+"/snap-stops", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeSnap(t *testing.T, rec *httptest.ResponseRecorder) snapResponse {
	t.Helper()
	var got snapResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

// The headline acceptance criterion: every stop comes back with a snapped
// coordinate, a chainage along the route, and an offset in metres.
//
// The first stop sits exactly on the route's first vertex, so it pins the
// degenerate case (chainage and offset both zero). The second sits 0.001° of
// latitude due north of that same vertex — off the near end of a line that runs
// south-east, so it clamps to the start and its offset is a known ~111 m. That
// magnitude is what proves the offset is reported in metres rather than
// degrees or kilometres.
func TestSnapStopsReturnsSnappedPointChainageAndOffset(t *testing.T) {
	store := seedSnapRoute(t)

	rec := postSnapStops(t, handler.SnapStops(store), "test-alignment", `{"stops":[
		{"id":"on-line","lat":37.79,"lng":-122.4},
		{"id":"north-of-start","lat":37.791,"lng":-122.4}
	]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}

	got := decodeSnap(t, rec)
	if got.RouteSlug != "test-alignment" {
		t.Errorf("route_slug = %q, want %q", got.RouteSlug, "test-alignment")
	}
	if len(got.Stops) != 2 {
		t.Fatalf("stops = %d, want 2", len(got.Stops))
	}

	onLine := got.Stops[0]
	if onLine.ID != "on-line" {
		t.Errorf("stops[0].id = %q, want the id the client supplied", onLine.ID)
	}
	if onLine.Index != 0 {
		t.Errorf("stops[0].index = %d, want 0", onLine.Index)
	}
	if onLine.Input.Lat != 37.79 || onLine.Input.Lng != -122.4 {
		t.Errorf("stops[0].input = %+v, want the raw point echoed back", onLine.Input)
	}
	if math.Abs(onLine.Snapped.Lat-37.79) > 1e-6 || math.Abs(onLine.Snapped.Lng-(-122.4)) > 1e-6 {
		t.Errorf("stops[0].snapped = %+v, want the vertex it already sits on", onLine.Snapped)
	}
	if onLine.ChainageM > 1 {
		t.Errorf("stops[0].chainage_m = %v, want ~0 at the start of the line", onLine.ChainageM)
	}
	if onLine.OffsetM > 1 {
		t.Errorf("stops[0].offset_m = %v, want ~0 for a stop already on the line", onLine.OffsetM)
	}

	// 0.001° of latitude is ~111 m; the stop clamps to the start vertex, so its
	// offset is that whole distance.
	north := got.Stops[1]
	if north.OffsetM < 105 || north.OffsetM > 118 {
		t.Errorf("stops[1].offset_m = %v, want ~111 (metres)", north.OffsetM)
	}
	if north.ChainageM > 1 {
		t.Errorf("stops[1].chainage_m = %v, want ~0: it snaps to the start vertex", north.ChainageM)
	}
	if north.OffRoute {
		t.Error("a stop ~111 m from the line must not be flagged off-route")
	}
}

// Chainage must grow along the line, so a stop near the far end reports a
// larger chainage than one near the start. This is what makes the value usable
// as "distance along the route" rather than an arbitrary index.
func TestSnapStopsChainageGrowsAlongTheLine(t *testing.T) {
	store := seedSnapRoute(t)

	rec := postSnapStops(t, handler.SnapStops(store), "test-alignment", `{"stops":[
		{"lat":37.79,"lng":-122.4},
		{"lat":37.70,"lng":-122.3},
		{"lat":37.60,"lng":-122.2}
	]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}

	got := decodeSnap(t, rec)
	if len(got.Stops) != 3 {
		t.Fatalf("stops = %d, want 3", len(got.Stops))
	}
	if !(got.Stops[0].ChainageM < got.Stops[1].ChainageM && got.Stops[1].ChainageM < got.Stops[2].ChainageM) {
		t.Errorf("chainages = %v, %v, %v; want strictly increasing along the line",
			got.Stops[0].ChainageM, got.Stops[1].ChainageM, got.Stops[2].ChainageM)
	}
	// The middle stop sits on the second vertex, roughly 13.3 km along a leg
	// spanning 0.1° of longitude and 0.09° of latitude. A wrong unit or a
	// per-leg (rather than cumulative) chainage would fall well outside this.
	if mid := got.Stops[1].ChainageM; mid < 13000 || mid > 13700 {
		t.Errorf("middle stop chainage_m = %v, want ~13300", mid)
	}
}

// The preview's whole job: an implausibly far stop is still answered, flagged
// rather than refused, so the user can see and fix it. Rejection at 500 m is
// the write path's concern (SPA-108), not this endpoint's.
func TestSnapStopsFlagsFarStopsWithoutFailing(t *testing.T) {
	store := seedSnapRoute(t)

	// 0.01° of latitude north of the start vertex is ~1.1 km off the alignment.
	rec := postSnapStops(t, handler.SnapStops(store), "test-alignment", `{"stops":[
		{"id":"near","lat":37.79,"lng":-122.4},
		{"id":"far","lat":37.80,"lng":-122.4}
	]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even with a stop far off the alignment; body %s",
			rec.Code, rec.Body.String())
	}

	got := decodeSnap(t, rec)
	if got.OffRouteThresholdM != 500 {
		t.Errorf("off_route_threshold_m = %v, want 500", got.OffRouteThresholdM)
	}
	if got.Stops[0].OffRoute {
		t.Error("the on-line stop must not be flagged off-route")
	}
	if !got.Stops[1].OffRoute {
		t.Errorf("a stop %v m off the line must be flagged off-route", got.Stops[1].OffsetM)
	}
	if got.Stops[1].OffsetM < 500 {
		t.Errorf("far stop offset_m = %v, want > 500", got.Stops[1].OffsetM)
	}
}

// Input order is preserved so the client can zip the response onto the list it
// sent; chainage order is reported separately so it can say "your stops are out
// of order along the line" without reordering anything itself.
func TestSnapStopsPreservesInputOrderAndReportsChainageOrder(t *testing.T) {
	store := seedSnapRoute(t)

	// Supplied last-to-first along the alignment.
	rec := postSnapStops(t, handler.SnapStops(store), "test-alignment", `{"stops":[
		{"id":"end","lat":37.60,"lng":-122.2},
		{"id":"start","lat":37.79,"lng":-122.4},
		{"id":"middle","lat":37.70,"lng":-122.3}
	]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}

	got := decodeSnap(t, rec)
	gotIDs := []string{got.Stops[0].ID, got.Stops[1].ID, got.Stops[2].ID}
	wantIDs := []string{"end", "start", "middle"}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Fatalf("stop ids = %v, want input order %v", gotIDs, wantIDs)
		}
	}

	// Sorted by chainage the order is start (1), middle (2), end (0).
	want := []int{1, 2, 0}
	if len(got.ChainageOrder) != len(want) {
		t.Fatalf("chainage_order = %v, want %v", got.ChainageOrder, want)
	}
	for i := range want {
		if got.ChainageOrder[i] != want[i] {
			t.Fatalf("chainage_order = %v, want %v", got.ChainageOrder, want)
		}
	}
	if got.OrderMatchesInput {
		t.Error("order_matches_input = true, but the stops were supplied against the line's direction")
	}
}

func TestSnapStopsReportsAgreementWhenStopsAreInChainageOrder(t *testing.T) {
	store := seedSnapRoute(t)

	rec := postSnapStops(t, handler.SnapStops(store), "test-alignment", `{"stops":[
		{"lat":37.79,"lng":-122.4},
		{"lat":37.70,"lng":-122.3}
	]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}

	got := decodeSnap(t, rec)
	if !got.OrderMatchesInput {
		t.Error("order_matches_input = false for stops supplied along the line's direction")
	}
	if len(got.ChainageOrder) != 2 || got.ChainageOrder[0] != 0 || got.ChainageOrder[1] != 1 {
		t.Errorf("chainage_order = %v, want [0 1]", got.ChainageOrder)
	}
}

// A single stop is a legitimate preview — the authoring UI snaps each stop as
// it is placed, long before there are two of them.
func TestSnapStopsAcceptsASingleStop(t *testing.T) {
	store := seedSnapRoute(t)

	rec := postSnapStops(t, handler.SnapStops(store), "test-alignment",
		`{"stops":[{"lat":37.70,"lng":-122.3}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a single stop; body %s", rec.Code, rec.Body.String())
	}

	got := decodeSnap(t, rec)
	if len(got.Stops) != 1 {
		t.Fatalf("stops = %d, want 1", len(got.Stops))
	}
	if len(got.ChainageOrder) != 1 || got.ChainageOrder[0] != 0 {
		t.Errorf("chainage_order = %v, want [0]", got.ChainageOrder)
	}
	if !got.OrderMatchesInput {
		t.Error("a lone stop is trivially in chainage order")
	}
}

func TestSnapStopsUnknownSlugIsNotFound(t *testing.T) {
	store := newFakeRouteStore()
	rec := postSnapStops(t, handler.SnapStops(store), "no-such-route",
		`{"stops":[{"lat":37.79,"lng":-122.4}]}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body %s", rec.Code, rec.Body.String())
	}
}

func TestSnapStopsRejectsInvalidPayloads(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"malformed json", `{"stops":`},
		{"no stops", `{"stops":[]}`},
		{"stops omitted", `{}`},
		{"latitude out of range", `{"stops":[{"lat":91,"lng":-122.4}]}`},
		{"longitude out of range", `{"stops":[{"lat":37.79,"lng":-181}]}`},
		// A misspelled coordinate key would otherwise decode to 0,0 — a stop in
		// the Gulf of Guinea, silently previewed as wildly off-route.
		{"misspelled coordinate key", `{"stops":[{"latitude":37.79,"lng":-122.4}]}`},
		{"unknown top-level field", `{"stops":[{"lat":37.79,"lng":-122.4}],"nonsense":true}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := seedSnapRoute(t)
			rec := postSnapStops(t, handler.SnapStops(store), "test-alignment", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestSnapStopsReportsStorageFailure(t *testing.T) {
	store := newFakeRouteStore()
	store.getErr = errors.New("database is down")

	rec := postSnapStops(t, handler.SnapStops(store), "whatever",
		`{"stops":[{"lat":37.79,"lng":-122.4}]}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "database is down") {
		t.Errorf("internal error leaked to client: %s", rec.Body.String())
	}
}

// Ingestion validates geometry, so a one-point route can only come from data
// that predates or bypassed that check. The client did nothing wrong, so it is
// a 500 rather than a 400 — but it must not be a panic or a bogus 200.
func TestSnapStopsRejectsUnusableRouteGeometry(t *testing.T) {
	store := newFakeRouteStore()
	store.routes["degenerate"] = transit.Route{
		Slug:     "degenerate",
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122.4, 37.79}}},
	}

	rec := postSnapStops(t, handler.SnapStops(store), "degenerate",
		`{"stops":[{"lat":37.79,"lng":-122.4}]}`)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 for a route whose geometry cannot be snapped to; body %s",
			rec.Code, rec.Body.String())
	}
}
