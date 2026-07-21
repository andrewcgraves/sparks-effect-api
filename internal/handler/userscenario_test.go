package handler_test

import (
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

// fakeScenarioStore is an in-memory handler.ScenarioStore.
type fakeScenarioStore struct {
	scenarios map[string]transit.UserScenario // keyed by ID
	services  map[string]string               // service id -> owner id
	failWith  error
}

func newFakeScenarioStore() *fakeScenarioStore {
	return &fakeScenarioStore{
		scenarios: map[string]transit.UserScenario{},
		services:  map[string]string{"svc-1": scnOwner.ID, "svc-2": scnOwner.ID, "svc-theirs": scnStranger.ID},
	}
}

func (f *fakeScenarioStore) CreateUserScenario(_ context.Context, sc transit.UserScenario) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.scenarios[sc.ID] = sc
	return nil
}

func (f *fakeScenarioStore) UpdateUserScenario(_ context.Context, sc transit.UserScenario) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.scenarios[sc.ID] = sc
	return nil
}

func (f *fakeScenarioStore) DeleteUserScenario(_ context.Context, id string) error {
	if f.failWith != nil {
		return f.failWith
	}
	delete(f.scenarios, id)
	return nil
}

func (f *fakeScenarioStore) GetUserScenarioByID(_ context.Context, id string) (transit.UserScenario, bool, error) {
	if f.failWith != nil {
		return transit.UserScenario{}, false, f.failWith
	}
	sc, ok := f.scenarios[id]
	return sc, ok, nil
}

func (f *fakeScenarioStore) GetUserScenarioBySlug(_ context.Context, slug string) (transit.UserScenario, bool, error) {
	if f.failWith != nil {
		return transit.UserScenario{}, false, f.failWith
	}
	for _, sc := range f.scenarios {
		if sc.Slug == slug {
			return sc, true, nil
		}
	}
	return transit.UserScenario{}, false, nil
}

func (f *fakeScenarioStore) ListUserScenariosByOwner(_ context.Context, ownerID string) ([]transit.UserScenario, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	out := []transit.UserScenario{}
	for _, sc := range f.scenarios {
		if sc.OwnerID == ownerID {
			out = append(out, sc)
		}
	}
	return out, nil
}

func (f *fakeScenarioStore) UserServiceIDsOwnedBy(_ context.Context, ownerID string, ids []string) (map[string]bool, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	out := map[string]bool{}
	for _, id := range ids {
		if owner, ok := f.services[id]; ok && owner == ownerID {
			out[id] = true
		}
	}
	return out, nil
}

// --- test harness ---

var (
	scnOwner    = transit.User{ID: "user-1", Email: "owner@example.com"}
	scnStranger = transit.User{ID: "user-2", Email: "stranger@example.com"}
	scnAdmin    = transit.User{ID: "user-3", Email: "admin@example.com", IsAdmin: true}
)

const scenarioCreatePayload = `{"name": "Weekend Getaway", "description": "Fri-Sun", "service_ids": ["svc-1", "svc-2"]}`

func scenarioMux(store handler.ScenarioStore) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/user-scenarios", handler.CreateUserScenario(store))
	mux.HandleFunc("GET /api/user-scenarios", handler.MyUserScenarios(store))
	mux.HandleFunc("GET /api/user-scenarios/{slug}", handler.GetUserScenario(store))
	mux.HandleFunc("PUT /api/user-scenarios/{slug}", handler.UpdateUserScenario(store))
	mux.HandleFunc("DELETE /api/user-scenarios/{slug}", handler.DeleteUserScenario(store))
	return mux
}

func scnServeAs(t *testing.T, store handler.ScenarioStore, user transit.User, method, target, body string) *httptest.ResponseRecorder {
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
	scenarioMux(store).ServeHTTP(rec, r)
	return rec
}

func decodeScenario(t *testing.T, rec *httptest.ResponseRecorder) transit.UserScenario {
	t.Helper()
	var sc transit.UserScenario
	if err := json.NewDecoder(rec.Body).Decode(&sc); err != nil {
		t.Fatalf("decoding response: %v (body %q)", err, rec.Body.String())
	}
	return sc
}

func seedScenarioRow(store *fakeScenarioStore, id, slug, owner string, serviceIDs []string) transit.UserScenario {
	sc := transit.UserScenario{
		ID: id, Slug: slug, OwnerID: owner, Name: "Seeded", ServiceIDs: serviceIDs,
	}
	store.scenarios[id] = sc
	return sc
}

// --- Create + read back, curated (not auto-included) membership (AC 1, 2) ---

func TestScenarioCreateAndReadBack(t *testing.T) {
	store := newFakeScenarioStore()

	rec := scnServeAs(t, store, scnOwner, http.MethodPost, "/api/user-scenarios", scenarioCreatePayload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body)
	}
	created := decodeScenario(t, rec)

	if created.ID == "" {
		t.Fatal("create: server must mint an id")
	}
	if created.Slug != "weekend-getaway" {
		t.Fatalf("create: got slug %q, want %q", created.Slug, "weekend-getaway")
	}
	if created.OwnerID != scnOwner.ID {
		t.Fatalf("create: got owner %q, want %q", created.OwnerID, scnOwner.ID)
	}
	if len(created.ServiceIDs) != 2 {
		t.Fatalf("create: got %d service ids, want 2", len(created.ServiceIDs))
	}
	if loc := rec.Header().Get("Location"); loc != "/api/user-scenarios/"+created.Slug {
		t.Fatalf("create: got Location %q, want %q", loc, "/api/user-scenarios/"+created.Slug)
	}

	rec = scnServeAs(t, store, scnOwner, http.MethodGet, "/api/user-scenarios/"+created.Slug, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("read back: got %d, want %d", rec.Code, http.StatusOK)
	}
	got := decodeScenario(t, rec)
	if got.ID != created.ID || got.Name != "Weekend Getaway" {
		t.Fatalf("read back: got %+v, want the created scenario", got)
	}
}

func TestScenarioOnlyExplicitServicesIncluded(t *testing.T) {
	// No auto-inclusion by matching id: only svc-1 was named, so svc-2 (also
	// owned by the same caller) must not silently appear in membership.
	store := newFakeScenarioStore()
	payload := `{"name": "Just One", "service_ids": ["svc-1"]}`

	rec := scnServeAs(t, store, scnOwner, http.MethodPost, "/api/user-scenarios", payload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body)
	}
	created := decodeScenario(t, rec)
	if len(created.ServiceIDs) != 1 || created.ServiceIDs[0] != "svc-1" {
		t.Fatalf("membership: got %v, want [svc-1] only", created.ServiceIDs)
	}
}

func TestScenarioCreateRejectsServiceNotOwnedByCaller(t *testing.T) {
	store := newFakeScenarioStore()
	payload := `{"name": "Sneaky", "service_ids": ["svc-1", "svc-theirs"]}`

	rec := scnServeAs(t, store, scnOwner, http.MethodPost, "/api/user-scenarios", payload)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want %d (body %s)", rec.Code, http.StatusUnprocessableEntity, rec.Body)
	}
}

// --- Ownership (AC 3) ---

func TestScenarioOnlyOwnerCanUpdateMembership(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "seeded", scnOwner.ID, []string{"svc-1"})

	body := `{"name": "Hijacked", "service_ids": ["svc-2"]}`
	rec := scnServeAs(t, store, scnStranger, http.MethodPut, "/api/user-scenarios/seeded", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stranger update: got %d, want %d", rec.Code, http.StatusNotFound)
	}
	if got := store.scenarios["scn-1"]; got.Name != "Seeded" || len(got.ServiceIDs) != 1 || got.ServiceIDs[0] != "svc-1" {
		t.Fatalf("stranger update mutated the scenario: %+v", got)
	}
}

func TestScenarioOnlyOwnerCanDelete(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "seeded", scnOwner.ID, []string{"svc-1"})

	rec := scnServeAs(t, store, scnStranger, http.MethodDelete, "/api/user-scenarios/seeded", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stranger delete: got %d, want %d", rec.Code, http.StatusNotFound)
	}
	if _, still := store.scenarios["scn-1"]; !still {
		t.Fatal("stranger delete removed the scenario")
	}
}

func TestScenarioStrangerCannotRead(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "seeded", scnOwner.ID, []string{"svc-1"})

	rec := scnServeAs(t, store, scnStranger, http.MethodGet, "/api/user-scenarios/seeded", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stranger read: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestScenarioAdminReachesAny(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "seeded", scnOwner.ID, []string{"svc-1"})

	rec := scnServeAs(t, store, scnAdmin, http.MethodGet, "/api/user-scenarios/seeded", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin read: got %d, want %d", rec.Code, http.StatusOK)
	}
	rec = scnServeAs(t, store, scnAdmin, http.MethodDelete, "/api/user-scenarios/seeded", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestScenarioOwnerCanUpdateMembership(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "seeded", scnOwner.ID, []string{"svc-1"})

	body := `{"name": "Renamed", "description": "New desc", "service_ids": ["svc-2"]}`
	rec := scnServeAs(t, store, scnOwner, http.MethodPut, "/api/user-scenarios/seeded", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner update: got %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body)
	}

	stored := store.scenarios["scn-1"]
	if stored.Name != "Renamed" || stored.Description != "New desc" {
		t.Errorf("update did not apply: %+v", stored)
	}
	if len(stored.ServiceIDs) != 1 || stored.ServiceIDs[0] != "svc-2" {
		t.Errorf("membership not replaced: %v", stored.ServiceIDs)
	}
	if stored.ID != "scn-1" || stored.Slug != "seeded" || stored.OwnerID != scnOwner.ID {
		t.Errorf("update changed server-owned fields: %+v", stored)
	}
}

func TestScenarioUpdateCannotReassignOwner(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "seeded", scnOwner.ID, []string{"svc-1"})

	body := `{"name": "Renamed", "owner_id": "user-99", "id": "scn-99", "slug": "hijacked", "service_ids": ["svc-1"]}`
	rec := scnServeAs(t, store, scnOwner, http.MethodPut, "/api/user-scenarios/seeded", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusOK)
	}
	stored := store.scenarios["scn-1"]
	if stored.OwnerID != scnOwner.ID || stored.ID != "scn-1" || stored.Slug != "seeded" {
		t.Fatalf("client overrode server-owned fields: %+v", stored)
	}
}

func TestScenarioCreateIgnoresClientSuppliedOwner(t *testing.T) {
	store := newFakeScenarioStore()
	body := `{"name": "Sneaky", "owner_id": "user-99", "service_ids": ["svc-1"]}`

	rec := scnServeAs(t, store, scnOwner, http.MethodPost, "/api/user-scenarios", body)
	created := decodeScenario(t, rec)
	if created.OwnerID != scnOwner.ID {
		t.Fatalf("got owner %q, want the authenticated caller %q", created.OwnerID, scnOwner.ID)
	}
}

func TestScenarioOwnerCanDelete(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "seeded", scnOwner.ID, []string{"svc-1"})

	rec := scnServeAs(t, store, scnOwner, http.MethodDelete, "/api/user-scenarios/seeded", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("owner delete: got %d, want %d", rec.Code, http.StatusNoContent)
	}
	if _, still := store.scenarios["scn-1"]; still {
		t.Fatal("owner delete did not remove the scenario")
	}
}

// --- Auth gating ---

func TestScenarioAnonymousIsRejectedOnEveryRoute(t *testing.T) {
	tests := []struct{ method, target, body string }{
		{http.MethodPost, "/api/user-scenarios", scenarioCreatePayload},
		{http.MethodGet, "/api/user-scenarios", ""},
		{http.MethodGet, "/api/user-scenarios/seeded", ""},
		{http.MethodPut, "/api/user-scenarios/seeded", scenarioCreatePayload},
		{http.MethodDelete, "/api/user-scenarios/seeded", ""},
	}
	for _, tc := range tests {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			store := newFakeScenarioStore()
			seedScenarioRow(store, "scn-1", "seeded", scnOwner.ID, []string{"svc-1"})

			rec := scnServeAs(t, store, transit.User{}, tc.method, tc.target, tc.body)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("got %d, want %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

// --- Listing ---

func TestScenarioListReturnsOnlyCallersScenarios(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "mine-a", scnOwner.ID, []string{"svc-1"})
	seedScenarioRow(store, "scn-2", "mine-b", scnOwner.ID, []string{"svc-2"})
	seedScenarioRow(store, "scn-3", "theirs", scnStranger.ID, []string{"svc-theirs"})

	rec := scnServeAs(t, store, scnOwner, http.MethodGet, "/api/user-scenarios", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusOK)
	}
	var got []transit.UserScenario
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d scenarios, want 2", len(got))
	}
	for _, sc := range got {
		if sc.OwnerID != scnOwner.ID {
			t.Fatalf("list leaked another user's scenario: %+v", sc)
		}
	}
}

func TestScenarioListReturnsEmptyArrayNotNull(t *testing.T) {
	rec := scnServeAs(t, newFakeScenarioStore(), scnOwner, http.MethodGet, "/api/user-scenarios", "")
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Fatalf("got body %q, want %q", body, "[]")
	}
}

// --- Validation and error mapping ---

func TestScenarioCreateRejectsInvalidPayloads(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"malformed json", `{not json`, http.StatusBadRequest},
		{"missing name", `{"service_ids": ["svc-1"]}`, http.StatusUnprocessableEntity},
		{"duplicate service id", `{"name": "X", "service_ids": ["svc-1", "svc-1"]}`, http.StatusUnprocessableEntity},
		{"unknown service id", `{"name": "X", "service_ids": ["svc-1", "ghost"]}`, http.StatusUnprocessableEntity},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := scnServeAs(t, newFakeScenarioStore(), scnOwner, http.MethodPost, "/api/user-scenarios", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("got %d, want %d (body %s)", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

func TestScenarioCreateAllowsEmptyMembership(t *testing.T) {
	rec := scnServeAs(t, newFakeScenarioStore(), scnOwner, http.MethodPost, "/api/user-scenarios", `{"name": "Empty Shell"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body)
	}
	if got := decodeScenario(t, rec).ServiceIDs; len(got) != 0 {
		t.Fatalf("service_ids: got %v, want empty", got)
	}
}

func TestScenarioMissingIs404(t *testing.T) {
	for _, tc := range []struct{ method, body string }{
		{http.MethodGet, ""},
		{http.MethodPut, scenarioCreatePayload},
		{http.MethodDelete, ""},
	} {
		t.Run(tc.method, func(t *testing.T) {
			rec := scnServeAs(t, newFakeScenarioStore(), scnOwner, tc.method, "/api/user-scenarios/ghost", tc.body)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("got %d, want %d", rec.Code, http.StatusNotFound)
			}
		})
	}
}

func TestScenarioRepositoryFailureIs500(t *testing.T) {
	store := newFakeScenarioStore()
	store.failWith = fmt.Errorf("database on fire")

	rec := scnServeAs(t, store, scnOwner, http.MethodPost, "/api/user-scenarios", scenarioCreatePayload)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if strings.Contains(rec.Body.String(), "on fire") {
		t.Fatalf("internal error leaked to client: %s", rec.Body)
	}
}

// --- Slug minting ---

func TestScenarioSlugCollisionGetsSuffix(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "weekend-getaway", scnStranger.ID, nil)

	rec := scnServeAs(t, store, scnOwner, http.MethodPost, "/api/user-scenarios", scenarioCreatePayload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusCreated)
	}
	if slug := decodeScenario(t, rec).Slug; slug != "weekend-getaway-2" {
		t.Fatalf("got slug %q, want %q", slug, "weekend-getaway-2")
	}
}
