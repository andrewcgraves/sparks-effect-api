package handler_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// storedStopSlugs reads the identities off what the store actually holds,
// rather than off the response, so these tests pin what was persisted.
func storedStopSlugs(t *testing.T, store *fakeServiceStore, id string) []string {
	t.Helper()
	svc, ok := store.services[id]
	if !ok {
		t.Fatalf("no service %q in store", id)
	}
	out := make([]string, len(svc.Stops))
	for i, stop := range svc.Stops {
		out[i] = stop.Slug
	}
	return out
}

func TestCreateMintsStopSlugs(t *testing.T) {
	store := newFakeServiceStore()

	rec := serveAs(t, store, svcOwner, http.MethodPost, "/api/services", createPayload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body)
	}
	created := decodeService(t, rec)

	want := []string{"bay-area-express--san-francisco", "bay-area-express--san-jose"}
	for i, got := range storedStopSlugs(t, store, created.ID) {
		if got != want[i] {
			t.Errorf("stored stop %d: got slug %q, want %q", i, got, want[i])
		}
	}
	// The response carries them too — a client that has to re-read to learn a
	// stop's identity would have no way to name one in a follow-up request.
	for i, stop := range created.Stops {
		if stop.Slug != want[i] {
			t.Errorf("response stop %d: got slug %q, want %q", i, stop.Slug, want[i])
		}
	}
}

// The stop identity is namespaced by the slug the service actually got, not by
// the one its name would suggest. Minting therefore has to happen after the
// collision suffix is settled, which is the ordering this pins.
func TestCreateNamespacesStopsByTheSlugTheServiceActuallyGot(t *testing.T) {
	store := newFakeServiceStore()
	seedService(store, "svc-existing", "bay-area-express", svcStranger.ID)

	rec := serveAs(t, store, svcOwner, http.MethodPost, "/api/services", createPayload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body)
	}
	created := decodeService(t, rec)
	if created.Slug != "bay-area-express-2" {
		t.Fatalf("setup: expected the collision suffix, got slug %q", created.Slug)
	}

	for i, got := range storedStopSlugs(t, store, created.ID) {
		if !strings.HasPrefix(got, "bay-area-express-2--") {
			t.Errorf("stop %d: got slug %q, want it namespaced by %q", i, got, created.Slug)
		}
	}
}

// A client that could name a stop could name another service's stop, and
// SPA-109 merges by identity. So the field is read-only on the wire.
func TestCreateIgnoresClientSuppliedStopSlugs(t *testing.T) {
	store := newFakeServiceStore()
	const payload = `{
		"route_slug": "sf-sj",
		"name": "Bay Area Express",
		"vehicle": {"max_speed_kmh": 320, "acceleration_ms2": 1.1, "deceleration_ms2": 1.3, "dwell_s": 45},
		"stops": [
			{"name": "San Francisco", "lat": 37.7749, "lng": -122.4194, "slug": "someone-elses--stop"},
			{"name": "San Jose", "lat": 37.3382, "lng": -121.8863, "slug": "hijacked"}
		],
		"frequency_windows": [{"start_time": "06:00", "end_time": "10:00", "headway_s": 900}]
	}`

	rec := serveAs(t, store, svcOwner, http.MethodPost, "/api/services", payload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body)
	}
	created := decodeService(t, rec)

	want := []string{"bay-area-express--san-francisco", "bay-area-express--san-jose"}
	for i, got := range storedStopSlugs(t, store, created.ID) {
		if got != want[i] {
			t.Errorf("stop %d: client-supplied slug survived as %q, want %q", i, got, want[i])
		}
	}
}

// An update rewrites the whole stop pattern, so identities have to be re-minted
// with it — otherwise a renamed or inserted stop keeps an identity that names
// something else.
func TestUpdateRemintsStopSlugs(t *testing.T) {
	store := newFakeServiceStore()
	seedService(store, "svc-1", "commuter-line", svcOwner.ID)

	const payload = `{
		"route_slug": "diagonal",
		"name": "Commuter Line",
		"vehicle": {"max_speed_kmh": 200, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 30},
		"stops": [
			{"name": "Alpha", "lat": 1, "lng": 1},
			{"name": "Beta", "lat": 2, "lng": 2},
			{"name": "Beta", "lat": 3, "lng": 3}
		],
		"frequency_windows": []
	}`

	rec := serveAs(t, store, svcOwner, http.MethodPut, "/api/services/commuter-line", payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: got %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body)
	}

	want := []string{"commuter-line--alpha", "commuter-line--beta", "commuter-line--beta-2"}
	for i, got := range storedStopSlugs(t, store, "svc-1") {
		if got != want[i] {
			t.Errorf("stop %d: got slug %q, want %q", i, got, want[i])
		}
	}
}

func TestUpdateIgnoresClientSuppliedStopSlugs(t *testing.T) {
	store := newFakeServiceStore()
	seedService(store, "svc-1", "commuter-line", svcOwner.ID)

	const payload = `{
		"route_slug": "diagonal",
		"name": "Commuter Line",
		"vehicle": {"max_speed_kmh": 200, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 30},
		"stops": [
			{"name": "Alpha", "lat": 1, "lng": 1, "slug": "other-service--alpha"},
			{"name": "Beta", "lat": 2, "lng": 2, "slug": ""}
		],
		"frequency_windows": []
	}`

	rec := serveAs(t, store, svcOwner, http.MethodPut, "/api/services/commuter-line", payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: got %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body)
	}

	want := []string{"commuter-line--alpha", "commuter-line--beta"}
	for i, got := range storedStopSlugs(t, store, "svc-1") {
		if got != want[i] {
			t.Errorf("stop %d: got %q, want %q", i, got, want[i])
		}
	}
}

// What the write path stores must be what the compiler derives. If these ever
// disagree, a stop is persisted under one identity and compiled under another,
// and the mismatch surfaces as the wrong stop being named in a compile result.
func TestStoredStopSlugsMatchWhatTheCompilerDerives(t *testing.T) {
	store := newFakeServiceStore()
	const payload = `{
		"route_slug": "diagonal",
		"name": "Loop Line",
		"vehicle": {"max_speed_kmh": 200, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 30},
		"stops": [
			{"name": "Central", "lat": 1, "lng": 1},
			{"name": "Central", "lat": 2, "lng": 2},
			{"name": "North", "lat": 3, "lng": 3}
		],
		"frequency_windows": []
	}`

	rec := serveAs(t, store, svcOwner, http.MethodPost, "/api/services", payload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body)
	}
	created := decodeService(t, rec)

	stored := store.services[created.ID]
	compilable, err := transit.CompilableFromUserService(store.routes["diagonal"], stored)
	if err != nil {
		t.Fatalf("CompilableFromUserService: %v", err)
	}
	if len(compilable.Stops) != len(stored.Stops) {
		t.Fatalf("compiled %d stops from %d stored", len(compilable.Stops), len(stored.Stops))
	}
	for i, cs := range compilable.Stops {
		if cs.Slug != stored.Stops[i].Slug {
			t.Errorf("stop %d: stored %q but compiled under %q", i, stored.Stops[i].Slug, cs.Slug)
		}
	}
}

// The seam where the service-slug collision suffix meets stop namespacing, end
// to end. mintSlug appends "-2" after slugifying, so two services sharing a
// maximum-length name get slugs of 80 and 82 characters — and anything that
// re-slugified the longer one would truncate the suffix back off and hand both
// services the same stop identities.
func TestCreateKeepsStopSlugsDistinctForMaxLengthServiceNames(t *testing.T) {
	store := newFakeServiceStore()
	payload := `{
		"route_slug": "diagonal",
		"name": "` + strings.Repeat("a", 80) + `",
		"vehicle": {"max_speed_kmh": 200, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 30},
		"stops": [{"name": "Downtown", "lat": 1, "lng": 1}, {"name": "Airport", "lat": 2, "lng": 2}],
		"frequency_windows": []
	}`

	first := decodeService(t, serveAs(t, store, svcOwner, http.MethodPost, "/api/services", payload))
	second := decodeService(t, serveAs(t, store, svcOwner, http.MethodPost, "/api/services", payload))
	if first.Slug == second.Slug {
		t.Fatalf("setup: both services got slug %q", first.Slug)
	}

	firstSlugs := storedStopSlugs(t, store, first.ID)
	secondSlugs := storedStopSlugs(t, store, second.ID)
	for i := range firstSlugs {
		if firstSlugs[i] == secondSlugs[i] {
			t.Errorf("stop %d: services %q and %q both minted identity %q",
				i, first.Slug, second.Slug, firstSlugs[i])
		}
	}
}
