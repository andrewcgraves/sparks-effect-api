package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	routes   map[string]bool
	failWith error
}

func newFakeServiceStore() *fakeServiceStore {
	return &fakeServiceStore{
		services: map[string]transit.UserService{},
		routes:   map[string]bool{"route-1": true, "route-2": true},
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

func (f *fakeServiceStore) RouteExists(_ context.Context, routeID string) (bool, error) {
	if f.failWith != nil {
		return false, f.failWith
	}
	return f.routes[routeID], nil
}

// --- test harness ---

var (
	svcOwner    = transit.User{ID: "user-1", Email: "owner@example.com"}
	svcStranger = transit.User{ID: "user-2", Email: "stranger@example.com"}
	svcAdmin    = transit.User{ID: "user-3", Email: "admin@example.com", IsAdmin: true}
)

const createPayload = `{
	"route_id": "route-1",
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
	if stored.Stops[0].Lat != 37.7749 || stored.Stops[0].Lng != -122.4194 {
		t.Errorf("stop coords not persisted: %+v", stored.Stops[0])
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
		"route_id": "route-1", "name": "Reordered",
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

	body := `{"route_id":"route-1","name":"Renamed by stranger",
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

	body := `{"route_id":"route-2","name":"Renamed",
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
	body := `{"route_id":"route-1","name":"Renamed","owner_id":"user-99","id":"svc-99","slug":"hijacked",
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
	body := `{"route_id":"route-1","name":"Sneaky","owner_id":"user-99",
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
		{"unknown route", strings.Replace(createPayload, "route-1", "route-nope", 1), http.StatusUnprocessableEntity},
		{"missing name", `{"route_id":"route-1","stops":[{"name":"A","lat":1,"lng":1},{"name":"B","lat":2,"lng":2}],
			"vehicle":{"max_speed_kmh":100,"acceleration_ms2":1,"deceleration_ms2":1}}`, http.StatusUnprocessableEntity},
		{"one stop", `{"route_id":"route-1","name":"X","stops":[{"name":"A","lat":1,"lng":1}],
			"vehicle":{"max_speed_kmh":100,"acceleration_ms2":1,"deceleration_ms2":1}}`, http.StatusUnprocessableEntity},
		{"bad vehicle", `{"route_id":"route-1","name":"X",
			"stops":[{"name":"A","lat":1,"lng":1},{"name":"B","lat":2,"lng":2}],
			"vehicle":{"max_speed_kmh":0,"acceleration_ms2":1,"deceleration_ms2":1}}`, http.StatusUnprocessableEntity},
		{"bad coords", `{"route_id":"route-1","name":"X",
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
	body := `{"route_id":"route-1","name":"` + string(big) + `"}`

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
