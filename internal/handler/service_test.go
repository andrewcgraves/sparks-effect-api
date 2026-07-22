package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// fakeServiceStore is an in-memory handler.ServiceStore.
type fakeServiceStore struct {
	services map[string]transit.UserService // keyed by ID
	routes   map[string]transit.Route       // keyed by slug
	failWith error
}

// newFakeServiceStore stocks two routes with real geometry, because stops are
// snapped onto it on every write and a route without an alignment cannot be
// authored against.
//
//   - "sf-sj" runs straight from San Francisco to San Jose, so createPayload's
//     two stops are its endpoints and snap to themselves.
//   - "diagonal" runs along lat == lng, so the small round coordinates the
//     ownership and validation tests use ((1,1), (2,2), …) all lie exactly on
//     it, in increasing order.
func newFakeServiceStore() *fakeServiceStore {
	return &fakeServiceStore{
		services: map[string]transit.UserService{},
		routes: map[string]transit.Route{
			"sf-sj": {
				ID: "route-1", Slug: "sf-sj", Name: "SF to San Jose",
				Geometry: transit.GeoLineString{
					Type:        "LineString",
					Coordinates: [][]float64{{-122.4194, 37.7749}, {-121.8863, 37.3382}},
				},
			},
			"diagonal": {
				ID: "route-2", Slug: "diagonal", Name: "Diagonal",
				Geometry: transit.GeoLineString{
					Type:        "LineString",
					Coordinates: [][]float64{{0, 0}, {80, 80}},
				},
			},
		},
	}
}

func (f *fakeServiceStore) CreateUserService(_ context.Context, svc transit.UserService) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.services[svc.ID] = svc
	return nil
}

func (f *fakeServiceStore) UpdateUserService(_ context.Context, svc transit.UserService) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.services[svc.ID] = svc
	return nil
}

func (f *fakeServiceStore) DeleteUserService(_ context.Context, id string) error {
	if f.failWith != nil {
		return f.failWith
	}
	delete(f.services, id)
	return nil
}

func (f *fakeServiceStore) GetUserServiceByID(_ context.Context, id string) (transit.UserService, bool, error) {
	if f.failWith != nil {
		return transit.UserService{}, false, f.failWith
	}
	svc, ok := f.services[id]
	return svc, ok, nil
}

func (f *fakeServiceStore) GetUserServiceBySlug(_ context.Context, slug string) (transit.UserService, bool, error) {
	if f.failWith != nil {
		return transit.UserService{}, false, f.failWith
	}
	for _, svc := range f.services {
		if svc.Slug == slug {
			return svc, true, nil
		}
	}
	return transit.UserService{}, false, nil
}

func (f *fakeServiceStore) ListUserServicesByOwner(_ context.Context, ownerID string) ([]transit.UserService, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	out := []transit.UserService{}
	for _, svc := range f.services {
		if svc.OwnerID == ownerID {
			out = append(out, svc)
		}
	}
	return out, nil
}

func (f *fakeServiceStore) GetRouteBySlug(_ context.Context, slug string) (transit.Route, bool, error) {
	if f.failWith != nil {
		return transit.Route{}, false, f.failWith
	}
	rt, ok := f.routes[slug]
	return rt, ok, nil
}

// --- test harness ---

var (
	svcOwner    = transit.User{ID: "user-1", Email: "owner@example.com"}
	svcStranger = transit.User{ID: "user-2", Email: "stranger@example.com"}
	svcAdmin    = transit.User{ID: "user-3", Email: "admin@example.com", IsAdmin: true}
)

const createPayload = `{
	"route_slug": "sf-sj",
	"name": "Bay Area Express",
	"vehicle": {"max_speed_kmh": 320, "acceleration_ms2": 1.1, "deceleration_ms2": 1.3, "dwell_s": 45},
	"stops": [
		{"name": "San Francisco", "lat": 37.7749, "lng": -122.4194},
		{"name": "San Jose", "lat": 37.3382, "lng": -121.8863}
	],
	"frequency_windows": [{"start_time": "06:00", "end_time": "10:00", "headway_s": 900}]
}`

// serviceMux registers the CRUD handlers without the auth middleware, so tests
// drive the handlers directly and inject identity via auth.WithUser — the same
// approach as mine_test.go. That the routes actually sit behind RequireAuth is
// covered in the server package.
func serviceMux(store handler.ServiceStore) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/services", handler.CreateService(store))
	mux.HandleFunc("GET /api/services", handler.MyUserServices(store))
	mux.HandleFunc("GET /api/services/{slug}", handler.GetService(store))
	mux.HandleFunc("PUT /api/services/{slug}", handler.UpdateService(store))
	mux.HandleFunc("DELETE /api/services/{slug}", handler.DeleteService(store))
	return mux
}

// serveAs routes a request as user. A zero-valued user is anonymous.
func serveAs(t *testing.T, store handler.ServiceStore, user transit.User, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if user.ID != "" {
		r = r.WithContext(auth.WithUser(r.Context(), user))
	}

	rec := httptest.NewRecorder()
	serviceMux(store).ServeHTTP(rec, r)
	return rec
}

func decodeService(t *testing.T, rec *httptest.ResponseRecorder) transit.UserService {
	t.Helper()
	var svc transit.UserService
	if err := json.NewDecoder(rec.Body).Decode(&svc); err != nil {
		t.Fatalf("decoding response: %v (body %q)", err, rec.Body.String())
	}
	return svc
}

// seedService puts a service owned by owner straight into the store.
func seedService(store *fakeServiceStore, id, slug, owner string) transit.UserService {
	svc := transit.UserService{
		ID: id, Slug: slug, OwnerID: owner, RouteID: "route-1", Name: "Seeded",
		Vehicle: transit.VehicleParams{MaxSpeedKMH: 200, AccelerationMS2: 1, DecelerationMS2: 1, DwellS: 30},
		Stops: []transit.ServiceStopPoint{
			{Name: "A", Lat: 1, Lng: 1, Seq: 0},
			{Name: "B", Lat: 2, Lng: 2, Seq: 1},
		},
	}
	store.services[id] = svc
	return svc
}

// --- Create + read back (AC 1) ---

func TestCreateAndReadBack(t *testing.T) {
	store := newFakeServiceStore()

	rec := serveAs(t, store, svcOwner, http.MethodPost, "/api/services", createPayload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body)
	}
	created := decodeService(t, rec)

	if created.ID == "" {
		t.Fatal("create: server must mint an ID")
	}
	if created.Slug != "bay-area-express" {
		t.Fatalf("create: got slug %q, want %q", created.Slug, "bay-area-express")
	}
	if created.OwnerID != svcOwner.ID {
		t.Fatalf("create: got owner %q, want %q", created.OwnerID, svcOwner.ID)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/services/"+created.Slug {
		t.Fatalf("create: got Location %q, want %q", loc, "/api/services/"+created.Slug)
	}

	rec = serveAs(t, store, svcOwner, http.MethodGet, "/api/services/"+created.Slug, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("read back: got %d, want %d", rec.Code, http.StatusOK)
	}
	got := decodeService(t, rec)
	if got.ID != created.ID || got.Name != "Bay Area Express" {
		t.Fatalf("read back: got %+v, want the created service", got)
	}
}

// --- Embedded ordering + params persist (AC 2) ---

func TestStopsAndParamsPersist(t *testing.T) {
	store := newFakeServiceStore()
	rec := serveAs(t, store, svcOwner, http.MethodPost, "/api/services", createPayload)
	created := decodeService(t, rec)

	stored := store.services[created.ID]

	if len(stored.Stops) != 2 {
		t.Fatalf("got %d stops, want 2", len(stored.Stops))
	}
	for i, want := range []string{"San Francisco", "San Jose"} {
		if stored.Stops[i].Name != want {
			t.Errorf("stop %d: got %q, want %q", i, stored.Stops[i].Name, want)
		}
		if stored.Stops[i].Seq != i {
			t.Errorf("stop %d: got seq %d, want %d", i, stored.Stops[i].Seq, i)
		}
	}
	// The stop is stored snapped, so its coordinate is the projection of what
	// was submitted rather than the submitted value itself. Here the stop is
	// the line's own endpoint, so the two agree to within the round trip
	// through the planar frame.
	if math.Abs(stored.Stops[0].Lat-37.7749) > 1e-9 || math.Abs(stored.Stops[0].Lng-(-122.4194)) > 1e-9 {
		t.Errorf("stop coords not persisted: %+v", stored.Stops[0])
	}
	if stored.Stops[0].OffsetM > 1e-6 {
		t.Errorf("stop on the alignment recorded offset %v, want ~0", stored.Stops[0].OffsetM)
	}
	if stored.Stops[1].ChainageM <= stored.Stops[0].ChainageM {
		t.Errorf("chainage not persisted in route order: %v then %v",
			stored.Stops[0].ChainageM, stored.Stops[1].ChainageM)
	}

	wantVehicle := transit.VehicleParams{
		MaxSpeedKMH: 320, AccelerationMS2: 1.1, DecelerationMS2: 1.3, DwellS: 45,
	}
	if stored.Vehicle != wantVehicle {
		t.Errorf("vehicle params: got %+v, want %+v", stored.Vehicle, wantVehicle)
	}

	if len(stored.FrequencyWindows) != 1 {
		t.Fatalf("got %d frequency windows, want 1", len(stored.FrequencyWindows))
	}
	if fw := stored.FrequencyWindows[0]; fw.HeadwayS != 900 || fw.StartTime != "06:00" || fw.EndTime != "10:00" {
		t.Errorf("frequency window: got %+v", fw)
	}
}

func TestStopOrderFollowsPayloadNotSeq(t *testing.T) {
	store := newFakeServiceStore()
	// Client sends misleading seq values; slice order must win.
	payload := `{
		"route_slug": "diagonal", "name": "Reordered",
		"vehicle": {"max_speed_kmh": 100, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 0},
		"stops": [
			{"name": "First",  "lat": 1, "lng": 1, "seq": 99},
			{"name": "Second", "lat": 2, "lng": 2, "seq": 0},
			{"name": "Third",  "lat": 3, "lng": 3, "seq": 50}
		]
	}`
	rec := serveAs(t, store, svcOwner, http.MethodPost, "/api/services", payload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body)
	}
	stored := store.services[decodeService(t, rec).ID]
	for i, want := range []string{"First", "Second", "Third"} {
		if stored.Stops[i].Name != want || stored.Stops[i].Seq != i {
			t.Fatalf("stop %d: got %s/seq=%d, want %s/seq=%d",
				i, stored.Stops[i].Name, stored.Stops[i].Seq, want, i)
		}
	}
}

// --- Ownership (AC 3) ---

func TestOnlyOwnerCanUpdate(t *testing.T) {
	store := newFakeServiceStore()
	seedService(store, "svc-1", "seeded", svcOwner.ID)

	body := `{"route_slug":"diagonal","name":"Renamed by stranger",
		"vehicle":{"max_speed_kmh":100,"acceleration_ms2":1,"deceleration_ms2":1,"dwell_s":0},
		"stops":[{"name":"A","lat":1,"lng":1},{"name":"B","lat":2,"lng":2}]}`

	rec := serveAs(t, store, svcStranger, http.MethodPut, "/api/services/seeded", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stranger update: got %d, want %d", rec.Code, http.StatusNotFound)
	}
	if store.services["svc-1"].Name != "Seeded" {
		t.Fatalf("stranger update mutated the service: %q", store.services["svc-1"].Name)
	}
}

func TestOnlyOwnerCanDelete(t *testing.T) {
	store := newFakeServiceStore()
	seedService(store, "svc-1", "seeded", svcOwner.ID)

	rec := serveAs(t, store, svcStranger, http.MethodDelete, "/api/services/seeded", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stranger delete: got %d, want %d", rec.Code, http.StatusNotFound)
	}
	if _, still := store.services["svc-1"]; !still {
		t.Fatal("stranger delete removed the service")
	}
}

func TestStrangerCannotReadService(t *testing.T) {
	store := newFakeServiceStore()
	seedService(store, "svc-1", "seeded", svcOwner.ID)

	rec := serveAs(t, store, svcStranger, http.MethodGet, "/api/services/seeded", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stranger read: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAdminReachesAnyService(t *testing.T) {
	// auth.CanAccess grants admins everything; these handlers must not
	// re-implement a stricter rule of their own.
	store := newFakeServiceStore()
	seedService(store, "svc-1", "seeded", svcOwner.ID)

	rec := serveAs(t, store, svcAdmin, http.MethodGet, "/api/services/seeded", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin read: got %d, want %d", rec.Code, http.StatusOK)
	}
	rec = serveAs(t, store, svcAdmin, http.MethodDelete, "/api/services/seeded", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestOwnerCanUpdate(t *testing.T) {
	store := newFakeServiceStore()
	seedService(store, "svc-1", "seeded", svcOwner.ID)

	body := `{"route_slug":"diagonal","name":"Renamed",
		"vehicle":{"max_speed_kmh":250,"acceleration_ms2":1.2,"deceleration_ms2":1.4,"dwell_s":60},
		"stops":[{"name":"X","lat":5,"lng":5},{"name":"Y","lat":6,"lng":6},{"name":"Z","lat":7,"lng":7}],
		"frequency_windows":[{"start_time":"07:00","end_time":"09:00","headway_s":600}]}`

	rec := serveAs(t, store, svcOwner, http.MethodPut, "/api/services/seeded", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner update: got %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body)
	}

	stored := store.services["svc-1"]
	if stored.Name != "Renamed" || stored.RouteID != "route-2" {
		t.Errorf("update did not apply: %+v", stored)
	}
	if len(stored.Stops) != 3 || stored.Stops[2].Name != "Z" {
		t.Errorf("stops not replaced: %+v", stored.Stops)
	}
	if stored.Vehicle.MaxSpeedKMH != 250 || stored.Vehicle.DwellS != 60 {
		t.Errorf("vehicle params not updated: %+v", stored.Vehicle)
	}
	if len(stored.FrequencyWindows) != 1 || stored.FrequencyWindows[0].HeadwayS != 600 {
		t.Errorf("frequency windows not updated: %+v", stored.FrequencyWindows)
	}
	// Identity is server-owned and must survive an update.
	if stored.ID != "svc-1" || stored.Slug != "seeded" || stored.OwnerID != svcOwner.ID {
		t.Errorf("update changed server-owned fields: %+v", stored)
	}
}

func TestOwnerCanDelete(t *testing.T) {
	store := newFakeServiceStore()
	seedService(store, "svc-1", "seeded", svcOwner.ID)

	rec := serveAs(t, store, svcOwner, http.MethodDelete, "/api/services/seeded", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("owner delete: got %d, want %d", rec.Code, http.StatusNoContent)
	}
	if _, still := store.services["svc-1"]; still {
		t.Fatal("owner delete did not remove the service")
	}
}

func TestUpdateCannotReassignOwner(t *testing.T) {
	store := newFakeServiceStore()
	seedService(store, "svc-1", "seeded", svcOwner.ID)

	// Client-supplied identity fields must be ignored, not honoured.
	body := `{"route_slug":"diagonal","name":"Renamed","owner_id":"user-99","id":"svc-99","slug":"hijacked",
		"vehicle":{"max_speed_kmh":100,"acceleration_ms2":1,"deceleration_ms2":1,"dwell_s":0},
		"stops":[{"name":"A","lat":1,"lng":1},{"name":"B","lat":2,"lng":2}]}`

	rec := serveAs(t, store, svcOwner, http.MethodPut, "/api/services/seeded", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusOK)
	}
	stored := store.services["svc-1"]
	if stored.OwnerID != svcOwner.ID || stored.ID != "svc-1" || stored.Slug != "seeded" {
		t.Fatalf("client overrode server-owned fields: %+v", stored)
	}
}

func TestCreateIgnoresClientSuppliedOwner(t *testing.T) {
	store := newFakeServiceStore()
	body := `{"route_slug":"diagonal","name":"Sneaky","owner_id":"user-99",
		"vehicle":{"max_speed_kmh":100,"acceleration_ms2":1,"deceleration_ms2":1,"dwell_s":0},
		"stops":[{"name":"A","lat":1,"lng":1},{"name":"B","lat":2,"lng":2}]}`

	rec := serveAs(t, store, svcOwner, http.MethodPost, "/api/services", body)
	created := decodeService(t, rec)
	if created.OwnerID != svcOwner.ID {
		t.Fatalf("got owner %q, want the authenticated caller %q", created.OwnerID, svcOwner.ID)
	}
}

// --- Auth gating ---

// TestAnonymousIsRejectedOnEveryRoute covers the handlers' own identity check.
// In the running server RequireAuth rejects first; this is defence in depth.
func TestAnonymousIsRejectedOnEveryRoute(t *testing.T) {
	tests := []struct {
		method, target, body string
	}{
		{http.MethodPost, "/api/services", createPayload},
		{http.MethodGet, "/api/services", ""},
		{http.MethodGet, "/api/services/seeded", ""},
		{http.MethodPut, "/api/services/seeded", createPayload},
		{http.MethodDelete, "/api/services/seeded", ""},
	}
	for _, tc := range tests {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			store := newFakeServiceStore()
			seedService(store, "svc-1", "seeded", svcOwner.ID)

			rec := serveAs(t, store, transit.User{}, tc.method, tc.target, tc.body)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("got %d, want %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

// --- Listing ---

func TestListReturnsOnlyCallersServices(t *testing.T) {
	store := newFakeServiceStore()
	seedService(store, "svc-1", "mine-a", svcOwner.ID)
	seedService(store, "svc-2", "mine-b", svcOwner.ID)
	seedService(store, "svc-3", "theirs", svcStranger.ID)

	rec := serveAs(t, store, svcOwner, http.MethodGet, "/api/services", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusOK)
	}
	var got []transit.UserService
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d services, want 2", len(got))
	}
	for _, svc := range got {
		if svc.OwnerID != svcOwner.ID {
			t.Fatalf("list leaked another user's service: %+v", svc)
		}
	}
}

func TestListIsScopedToOwnRowsForAdminsToo(t *testing.T) {
	// "Mine" means mine even for an admin — admin rights gate privileged
	// endpoints, they do not redefine ownership. Matches MyServices.
	store := newFakeServiceStore()
	seedService(store, "svc-1", "mine", svcOwner.ID)

	rec := serveAs(t, store, svcAdmin, http.MethodGet, "/api/services", "")
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Fatalf("admin list: got %q, want %q", body, "[]")
	}
}

func TestListReturnsEmptyArrayNotNull(t *testing.T) {
	rec := serveAs(t, newFakeServiceStore(), svcOwner, http.MethodGet, "/api/services", "")
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Fatalf("got body %q, want %q", body, "[]")
	}
}

// --- Validation and error mapping ---

func TestCreateRejectsInvalidPayloads(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"malformed json", `{not json`, http.StatusBadRequest},
		{"unknown route", strings.Replace(createPayload, "sf-sj", "route-nope", 1), http.StatusUnprocessableEntity},
		{"missing name", `{"route_slug":"diagonal","stops":[{"name":"A","lat":1,"lng":1},{"name":"B","lat":2,"lng":2}],
			"vehicle":{"max_speed_kmh":100,"acceleration_ms2":1,"deceleration_ms2":1}}`, http.StatusUnprocessableEntity},
		{"one stop", `{"route_slug":"diagonal","name":"X","stops":[{"name":"A","lat":1,"lng":1}],
			"vehicle":{"max_speed_kmh":100,"acceleration_ms2":1,"deceleration_ms2":1}}`, http.StatusUnprocessableEntity},
		{"bad vehicle", `{"route_slug":"diagonal","name":"X",
			"stops":[{"name":"A","lat":1,"lng":1},{"name":"B","lat":2,"lng":2}],
			"vehicle":{"max_speed_kmh":0,"acceleration_ms2":1,"deceleration_ms2":1}}`, http.StatusUnprocessableEntity},
		{"bad coords", `{"route_slug":"diagonal","name":"X",
			"stops":[{"name":"A","lat":999,"lng":1},{"name":"B","lat":2,"lng":2}],
			"vehicle":{"max_speed_kmh":100,"acceleration_ms2":1,"deceleration_ms2":1}}`, http.StatusUnprocessableEntity},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := serveAs(t, newFakeServiceStore(), svcOwner, http.MethodPost, "/api/services", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("got %d, want %d (body %s)", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

func TestMissingServiceIs404(t *testing.T) {
	for _, tc := range []struct{ method, body string }{
		{http.MethodGet, ""},
		{http.MethodPut, createPayload},
		{http.MethodDelete, ""},
	} {
		t.Run(tc.method, func(t *testing.T) {
			rec := serveAs(t, newFakeServiceStore(), svcOwner, tc.method, "/api/services/ghost", tc.body)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("got %d, want %d", rec.Code, http.StatusNotFound)
			}
		})
	}
}

func TestRepositoryFailureIs500(t *testing.T) {
	store := newFakeServiceStore()
	store.failWith = fmt.Errorf("database on fire")

	rec := serveAs(t, store, svcOwner, http.MethodPost, "/api/services", createPayload)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	// The raw error must not reach the client.
	if strings.Contains(rec.Body.String(), "on fire") {
		t.Fatalf("internal error leaked to client: %s", rec.Body)
	}
}

func TestOversizedBodyIsRejected(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 2<<20)
	body := `{"route_slug":"diagonal","name":"` + string(big) + `"}`

	rec := serveAs(t, newFakeServiceStore(), svcOwner, http.MethodPost, "/api/services", body)
	if rec.Code != http.StatusRequestEntityTooLarge && rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 413 or 400", rec.Code)
	}
}

// --- Slug minting ---

func TestSlugCollisionGetsSuffix(t *testing.T) {
	store := newFakeServiceStore()
	seedService(store, "svc-1", "bay-area-express", svcStranger.ID)

	rec := serveAs(t, store, svcOwner, http.MethodPost, "/api/services", createPayload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusCreated)
	}
	if slug := decodeService(t, rec).Slug; slug != "bay-area-express-2" {
		t.Fatalf("got slug %q, want %q", slug, "bay-area-express-2")
	}
}

// --- Snapping on write (SPA-108) ---

// snapPayload builds a create/update body on route with the given stops, each
// "name,lat,lng".
func snapPayload(routeSlug string, stops ...[3]string) string {
	parts := make([]string, len(stops))
	for i, s := range stops {
		parts[i] = fmt.Sprintf(`{"name":%q,"lat":%s,"lng":%s}`, s[0], s[1], s[2])
	}
	return fmt.Sprintf(`{"route_slug":%q,"name":"Snapped",
		"vehicle":{"max_speed_kmh":200,"acceleration_ms2":1,"deceleration_ms2":1,"dwell_s":30},
		"stops":[%s]}`, routeSlug, strings.Join(parts, ","))
}

func TestCreateRejectsAStopOffTheAlignment(t *testing.T) {
	store := newFakeServiceStore()
	// The diagonal runs along lat == lng; (10, 20) is nowhere near it.
	body := snapPayload("diagonal", [3]string{"On line", "1", "1"}, [3]string{"Gilroy", "10", "20"})

	rec := serveAs(t, store, svcOwner, http.MethodPost, "/api/services", body)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want %d (body %s)", rec.Code, http.StatusUnprocessableEntity, rec.Body)
	}
	for _, want := range []string{"Gilroy", "diagonal", "km"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("error body %s does not name %q", rec.Body, want)
		}
	}
	if len(store.services) != 0 {
		t.Fatalf("a rejected service was stored anyway: %+v", store.services)
	}
}

func TestCreateRejectsStopsOutOfChainageOrder(t *testing.T) {
	store := newFakeServiceStore()
	// Authored A→C→B, but C lies beyond B along the line.
	body := snapPayload("diagonal",
		[3]string{"A", "1", "1"}, [3]string{"C", "3", "3"}, [3]string{"B", "2", "2"})

	rec := serveAs(t, store, svcOwner, http.MethodPost, "/api/services", body)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want %d (body %s)", rec.Code, http.StatusUnprocessableEntity, rec.Body)
	}
	for _, want := range []string{`\"C\"`, `\"B\"`, "seq 1", "seq 2"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("error body %s does not name %s", rec.Body, want)
		}
	}
	if len(store.services) != 0 {
		t.Fatalf("a rejected service was stored anyway: %+v", store.services)
	}
}

func TestCreateRequiresARouteSlug(t *testing.T) {
	body := `{"name":"No route","vehicle":{"max_speed_kmh":100,"acceleration_ms2":1,"deceleration_ms2":1},
		"stops":[{"name":"A","lat":1,"lng":1},{"name":"B","lat":2,"lng":2}]}`

	rec := serveAs(t, newFakeServiceStore(), svcOwner, http.MethodPost, "/api/services", body)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want %d (body %s)", rec.Code, http.StatusUnprocessableEntity, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "route_slug") {
		t.Errorf("error body %s does not name route_slug", rec.Body)
	}
}

// TestCreateResolvesTheRouteSlugToItsID pins that the client names a route by
// slug and never supplies an ID: what is stored is the ID the server resolved.
func TestCreateResolvesTheRouteSlugToItsID(t *testing.T) {
	store := newFakeServiceStore()
	rec := serveAs(t, store, svcOwner, http.MethodPost, "/api/services", createPayload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body)
	}
	if stored := store.services[decodeService(t, rec).ID]; stored.RouteID != "route-1" {
		t.Fatalf("got route id %q, want the id behind slug sf-sj", stored.RouteID)
	}
}

// TestCreateResponseCarriesTheSnap covers the client that skips the preview:
// the create response alone must show where each stop landed.
func TestCreateResponseCarriesTheSnap(t *testing.T) {
	store := newFakeServiceStore()
	// Both stops sit a little off the diagonal, so offsets are non-zero.
	body := snapPayload("diagonal", [3]string{"A", "1.0", "1.001"}, [3]string{"B", "2.0", "2.001"})

	rec := serveAs(t, store, svcOwner, http.MethodPost, "/api/services", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body)
	}

	var got struct {
		Stops []struct {
			Name      string  `json:"name"`
			Lat       float64 `json:"lat"`
			Lng       float64 `json:"lng"`
			ChainageM float64 `json:"chainage_m"`
			OffsetM   float64 `json:"offset_m"`
		} `json:"stops"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v (body %s)", err, rec.Body)
	}
	if len(got.Stops) != 2 {
		t.Fatalf("got %d stops in the response, want 2", len(got.Stops))
	}
	for _, stop := range got.Stops {
		if stop.OffsetM <= 0 {
			t.Errorf("stop %q: response reports offset %v, want the distance it moved", stop.Name, stop.OffsetM)
		}
		// Snapped onto lat == lng, so the reported position is on the line.
		if math.Abs(stop.Lat-stop.Lng) > 1e-6 {
			t.Errorf("stop %q: response reports %v,%v, which is not on the alignment", stop.Name, stop.Lat, stop.Lng)
		}
	}
	if got.Stops[1].ChainageM <= got.Stops[0].ChainageM {
		t.Errorf("response chainage does not advance along the route: %v then %v",
			got.Stops[0].ChainageM, got.Stops[1].ChainageM)
	}
}

func TestUpdateSnapsStopsToo(t *testing.T) {
	store := newFakeServiceStore()
	seedService(store, "svc-1", "seeded", svcOwner.ID)

	body := snapPayload("diagonal", [3]string{"A", "1.0", "1.001"}, [3]string{"B", "2.0", "2.001"})
	rec := serveAs(t, store, svcOwner, http.MethodPut, "/api/services/seeded", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body)
	}

	stored := store.services["svc-1"]
	for _, stop := range stored.Stops {
		if math.Abs(stop.Lat-stop.Lng) > 1e-6 {
			t.Errorf("update stored an unsnapped stop: %+v", stop)
		}
		if stop.OffsetM <= 0 {
			t.Errorf("update did not record how far stop %q moved: %+v", stop.Name, stop)
		}
	}
}

func TestUpdateRejectsAnOffRouteStop(t *testing.T) {
	store := newFakeServiceStore()
	seedService(store, "svc-1", "seeded", svcOwner.ID)
	before := store.services["svc-1"]

	body := snapPayload("diagonal", [3]string{"A", "1", "1"}, [3]string{"Gilroy", "10", "20"})
	rec := serveAs(t, store, svcOwner, http.MethodPut, "/api/services/seeded", body)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want %d (body %s)", rec.Code, http.StatusUnprocessableEntity, rec.Body)
	}
	if store.services["svc-1"].Name != before.Name {
		t.Fatalf("a rejected update was applied anyway: %+v", store.services["svc-1"])
	}
}

// TestResavingAServiceUnchangedDoesNotMoveItsStops is the idempotency
// guarantee: the coordinates a create returns, sent straight back as an update,
// must land in the same place.
func TestResavingAServiceUnchangedDoesNotMoveItsStops(t *testing.T) {
	store := newFakeServiceStore()
	rec := serveAs(t, store, svcOwner, http.MethodPost, "/api/services",
		snapPayload("diagonal", [3]string{"A", "1.0", "1.001"}, [3]string{"B", "2.0", "2.001"}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body)
	}
	created := decodeService(t, rec)

	// Echo the created service back verbatim, as a client editing an unrelated
	// field would.
	body, err := json.Marshal(map[string]any{
		"route_slug": "diagonal",
		"name":       created.Name,
		"vehicle":    created.Vehicle,
		"stops":      created.Stops,
	})
	if err != nil {
		t.Fatalf("marshalling update: %v", err)
	}

	rec = serveAs(t, store, svcOwner, http.MethodPut, "/api/services/"+created.Slug, string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("update: got %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body)
	}

	updated := decodeService(t, rec)
	for i, stop := range updated.Stops {
		was := created.Stops[i]
		if math.Abs(stop.Lat-was.Lat) > 1e-9 || math.Abs(stop.Lng-was.Lng) > 1e-9 {
			t.Errorf("stop %q drifted on re-save: %v,%v became %v,%v",
				stop.Name, was.Lat, was.Lng, stop.Lat, stop.Lng)
		}
		if math.Abs(stop.ChainageM-was.ChainageM) > 1e-6 {
			t.Errorf("stop %q chainage drifted on re-save: %v became %v",
				stop.Name, was.ChainageM, stop.ChainageM)
		}
	}
}

func TestUnusableRouteGeometryIs500(t *testing.T) {
	store := newFakeServiceStore()
	store.routes["degenerate"] = transit.Route{
		ID: "route-3", Slug: "degenerate",
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{0, 0}}},
	}

	body := snapPayload("degenerate", [3]string{"A", "1", "1"}, [3]string{"B", "2", "2"})
	rec := serveAs(t, store, svcOwner, http.MethodPost, "/api/services", body)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want %d (body %s)", rec.Code, http.StatusInternalServerError, rec.Body)
	}
}
