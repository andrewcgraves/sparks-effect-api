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

const (
	ownedSvcID    = "00000000-0000-4104-8000-000000000001"
	vehicleTypeID = "00000000-0000-4105-8000-000000000001"
)

// fakeOwnedServiceStore is an in-memory handler.OwnedServiceStore.
type fakeOwnedServiceStore struct {
	services  map[string]transit.Service // by id
	scenarios map[string]transit.Scenario
	routes    map[string]transit.Route
	stations  map[string][]transit.Station // by scenario id
	vehicles  map[string]transit.VehicleType
	failWith  error
}

func newFakeOwnedServiceStore() *fakeOwnedServiceStore {
	return &fakeOwnedServiceStore{
		services: map[string]transit.Service{},
		scenarios: map[string]transit.Scenario{
			"ca-hsr":  {ID: "00000000-0000-4001-8001-000000000001", Slug: "ca-hsr", Name: "CA HSR"},
			"a-draft": {ID: ownedScrID, Slug: "a-draft", Name: "A Draft", OwnerID: ptrTo(ownerAID)},
			"b-draft": {ID: "00000000-0000-4101-8000-000000000002", Slug: "b-draft", OwnerID: ptrTo(ownerBID)},
		},
		routes: map[string]transit.Route{
			"curated-line": {ID: "00000000-0000-4002-8000-000000000001", Slug: "curated-line"},
			"a-line":       {ID: "00000000-0000-4002-8000-000000000002", Slug: "a-line", OwnerID: ptrTo(ownerAID)},
			"b-line":       {ID: "00000000-0000-4002-8000-000000000003", Slug: "b-line", OwnerID: ptrTo(ownerBID)},
		},
		stations: map[string][]transit.Station{
			ownedScrID: {
				{ID: "st-1", ScenarioID: ownedScrID, Slug: "west-end", OwnerID: ptrTo(ownerAID)},
				{ID: "st-2", ScenarioID: ownedScrID, Slug: "east-end", OwnerID: ptrTo(ownerAID)},
			},
			// Another scenario's stations, to prove they cannot be borrowed.
			"00000000-0000-4101-8000-000000000002": {
				{ID: "st-9", Slug: "elsewhere", OwnerID: ptrTo(ownerBID)},
			},
		},
		vehicles: map[string]transit.VehicleType{
			vehicleTypeID: {ID: vehicleTypeID, Name: "Trainset"},
		},
	}
}

func (f *fakeOwnedServiceStore) CreateService(_ context.Context, svc transit.Service) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.services[svc.ID] = svc
	return nil
}

func (f *fakeOwnedServiceStore) GetServiceByID(_ context.Context, id string) (transit.Service, bool, error) {
	if f.failWith != nil {
		return transit.Service{}, false, f.failWith
	}
	svc, ok := f.services[id]
	return svc, ok, nil
}

func (f *fakeOwnedServiceStore) UpdateService(_ context.Context, svc transit.Service) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.services[svc.ID] = svc
	return nil
}

func (f *fakeOwnedServiceStore) DeleteService(_ context.Context, id string) error {
	if f.failWith != nil {
		return f.failWith
	}
	delete(f.services, id)
	return nil
}

func (f *fakeOwnedServiceStore) GetScenarioBySlug(_ context.Context, slug string) (transit.Scenario, bool, error) {
	sc, ok := f.scenarios[slug]
	return sc, ok, nil
}

func (f *fakeOwnedServiceStore) GetRouteBySlug(_ context.Context, slug string) (transit.Route, bool, error) {
	rt, ok := f.routes[slug]
	return rt, ok, nil
}

func (f *fakeOwnedServiceStore) GetVehicleTypeByID(_ context.Context, id string) (transit.VehicleType, bool, error) {
	vt, ok := f.vehicles[id]
	return vt, ok, nil
}

func (f *fakeOwnedServiceStore) ListStationsByScenario(_ context.Context, scenarioID string) ([]transit.Station, error) {
	return f.stations[scenarioID], nil
}

// serviceBody builds a create/update payload with the parts a test varies.
func serviceBody(scenarioSlug, routeSlug, vehicleID string, stopSlugs ...string) string {
	stops := make([]string, len(stopSlugs))
	for i, slug := range stopSlugs {
		stops[i] = fmt.Sprintf(`{"station_slug":%q,"sequence":%d}`, slug, i+1)
	}
	return `{"scenario_slug":"` + scenarioSlug + `","route_slug":"` + routeSlug + `",
		"vehicle_type_id":"` + vehicleID + `","name":"Local","direction":"eastbound",
		"stops":[` + strings.Join(stops, ",") + `],
		"frequency_windows":[{"start_time":"06:00","end_time":"22:00","headway_s":1800}]}`
}

func asServiceUser(t *testing.T, h http.HandlerFunc, user transit.User, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	id := strings.Trim(strings.TrimPrefix(target, "/api/me/services"), "/")
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.SetPathValue("id", id)
	req = req.WithContext(auth.WithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestCreateOwnedServiceStampsTheCallerAsOwner(t *testing.T) {
	store := newFakeOwnedServiceStore()

	rec := asServiceUser(t, handler.CreateOwnedService(store, transit.DefaultBoardingWaitPolicy()), memberA, http.MethodPost,
		"/api/me/services", serviceBody("a-draft", "a-line", vehicleTypeID, "west-end", "east-end"))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d (%s)", rec.Code, rec.Body)
	}
	var got transit.Service
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.OwnerID == nil || *got.OwnerID != ownerAID {
		t.Errorf("owner: want %q, got %v", ownerAID, got.OwnerID)
	}
	if got.ScenarioID != ownedScrID {
		t.Errorf("scenario: want the caller's own, got %q", got.ScenarioID)
	}
	if len(got.Stops) != 2 || got.Stops[0].StationID != "st-1" {
		t.Errorf("stops not resolved to station ids: %+v", got.Stops)
	}
}

// A curated route is a public building block; someone else's private draft is
// not. This is the CanReference / CanAccess split, and it is easy to get
// backwards.
func TestCreateOwnedServiceReferencesCuratedRoutesButNotPrivateOnes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		routeSlug string
		want      int
	}{
		{"a curated alignment", "curated-line", http.StatusCreated},
		{"the caller's own", "a-line", http.StatusCreated},
		{"someone else's private draft", "b-line", http.StatusUnprocessableEntity},
		{"one that does not exist", "nope", http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeOwnedServiceStore()
			rec := asServiceUser(t, handler.CreateOwnedService(store, transit.DefaultBoardingWaitPolicy()), memberA, http.MethodPost,
				"/api/me/services", serviceBody("a-draft", tc.routeSlug, vehicleTypeID, "west-end", "east-end"))
			if rec.Code != tc.want {
				t.Errorf("status: want %d, got %d (%s)", tc.want, rec.Code, rec.Body)
			}
		})
	}
}

// Authoring a service into a scenario is mutating that scenario, so unlike
// referencing a route it stays admin-only for curated data.
func TestCreateOwnedServiceRefusesAScenarioTheCallerDoesNotOwn(t *testing.T) {
	for _, tc := range []struct {
		name         string
		scenarioSlug string
		want         int
	}{
		{"their own", "a-draft", http.StatusCreated},
		{"the curated baseline", "ca-hsr", http.StatusUnprocessableEntity},
		{"someone else's", "b-draft", http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeOwnedServiceStore()
			rec := asServiceUser(t, handler.CreateOwnedService(store, transit.DefaultBoardingWaitPolicy()), memberA, http.MethodPost,
				"/api/me/services", serviceBody(tc.scenarioSlug, "curated-line", vehicleTypeID, "west-end", "east-end"))
			if rec.Code != tc.want {
				t.Errorf("status: want %d, got %d (%s)", tc.want, rec.Code, rec.Body)
			}
		})
	}
}

// Every NOT NULL foreign key gets a 422 naming the offending value rather than
// an opaque 500 from the constraint.
func TestCreateOwnedServiceRejectsUnknownReferences(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantIn     string
	}{
		{
			"an unknown vehicle type",
			serviceBody("a-draft", "a-line", "no-such-vehicle", "west-end", "east-end"),
			"vehicle_type_id",
		},
		{
			"an unknown station",
			serviceBody("a-draft", "a-line", vehicleTypeID, "west-end", "nowhere"),
			"nowhere",
		},
		{
			"a station belonging to a different scenario",
			serviceBody("a-draft", "a-line", vehicleTypeID, "west-end", "elsewhere"),
			"elsewhere",
		},
		{
			"the same station twice",
			serviceBody("a-draft", "a-line", vehicleTypeID, "west-end", "west-end"),
			"twice",
		},
		{
			"a single stop",
			serviceBody("a-draft", "a-line", vehicleTypeID, "west-end"),
			"at least two stops",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeOwnedServiceStore()
			rec := asServiceUser(t, handler.CreateOwnedService(store, transit.DefaultBoardingWaitPolicy()), memberA,
				http.MethodPost, "/api/me/services", tc.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status: want 422, got %d (%s)", rec.Code, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tc.wantIn) {
				t.Errorf("body %s: want it to name %q", rec.Body, tc.wantIn)
			}
		})
	}
}

// provenance is an editorial claim about where a service's numbers came from,
// so a user cannot make it about their own work.
func TestCreateOwnedServiceIgnoresClientSuppliedProvenance(t *testing.T) {
	store := newFakeOwnedServiceStore()

	body := `{"scenario_slug":"a-draft","route_slug":"a-line","vehicle_type_id":"` + vehicleTypeID + `",
		"name":"Local","provenance":"` + transit.ProvenanceCalibrated + `",
		"stops":[{"station_slug":"west-end","sequence":1},{"station_slug":"east-end","sequence":2}]}`
	rec := asServiceUser(t, handler.CreateOwnedService(store, transit.DefaultBoardingWaitPolicy()), memberA,
		http.MethodPost, "/api/me/services", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d (%s)", rec.Code, rec.Body)
	}

	var got transit.Service
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Provenance != "" {
		t.Errorf("provenance: want it empty, got %q", got.Provenance)
	}
}

// Sequence numbers are renumbered from the caller's ordering: the PK is
// (service_id, sequence), so sparse or duplicate numbers from a client would be
// a constraint violation or a gap the compiler misreads.
func TestCreateOwnedServiceRenumbersStopSequences(t *testing.T) {
	store := newFakeOwnedServiceStore()

	body := `{"scenario_slug":"a-draft","route_slug":"a-line","vehicle_type_id":"` + vehicleTypeID + `",
		"name":"Local","stops":[
			{"station_slug":"east-end","sequence":70},
			{"station_slug":"west-end","sequence":20}]}`
	rec := asServiceUser(t, handler.CreateOwnedService(store, transit.DefaultBoardingWaitPolicy()), memberA,
		http.MethodPost, "/api/me/services", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d (%s)", rec.Code, rec.Body)
	}

	var got transit.Service
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Stops) != 2 {
		t.Fatalf("stops: want 2, got %+v", got.Stops)
	}
	// Ordered by the caller's numbering, then renumbered 1..n.
	if got.Stops[0].StationID != "st-1" || got.Stops[0].Sequence != 1 {
		t.Errorf("stop 0: want west-end at sequence 1, got %+v", got.Stops[0])
	}
	if got.Stops[1].StationID != "st-2" || got.Stops[1].Sequence != 2 {
		t.Errorf("stop 1: want east-end at sequence 2, got %+v", got.Stops[1])
	}
}

func TestOwnedServiceAnswers404ToStrangers(t *testing.T) {
	seed := func() *fakeOwnedServiceStore {
		store := newFakeOwnedServiceStore()
		store.services[ownedSvcID] = transit.Service{
			ID: ownedSvcID, ScenarioID: ownedScrID, Name: "Local", OwnerID: ptrTo(ownerAID),
		}
		return store
	}

	for _, tc := range []struct {
		name string
		user transit.User
		want int
	}{
		{"its owner", memberA, http.StatusOK},
		{"a stranger", memberB, http.StatusNotFound},
		{"an admin", adminU, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := asServiceUser(t, handler.GetOwnedService(seed(), transit.DefaultBoardingWaitPolicy()),
				tc.user, http.MethodGet, "/api/me/services/"+ownedSvcID, "")
			if rec.Code != tc.want {
				t.Errorf("status: want %d, got %d", tc.want, rec.Code)
			}
		})
	}
}

// A curated seeded service has no owner, so it stays read-only for everyone but
// admins — the ca-hsr services included.
func TestCuratedServiceIsNotEditableByANonAdmin(t *testing.T) {
	store := newFakeOwnedServiceStore()
	store.services["curated-svc"] = transit.Service{ID: "curated-svc", Name: "HSR Express"}

	rec := asServiceUser(t, handler.DeleteOwnedService(store), memberA,
		http.MethodDelete, "/api/me/services/curated-svc", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("member deleting a curated service: want 404, got %d", rec.Code)
	}
	if _, still := store.services["curated-svc"]; !still {
		t.Error("the curated service was deleted")
	}
}

func TestUpdateOwnedServiceKeepsIdentity(t *testing.T) {
	store := newFakeOwnedServiceStore()
	store.services[ownedSvcID] = transit.Service{
		ID: ownedSvcID, ScenarioID: ownedScrID, Name: "Local", OwnerID: ptrTo(ownerAID),
	}

	rec := asServiceUser(t, handler.UpdateOwnedService(store, transit.DefaultBoardingWaitPolicy()), memberA, http.MethodPut,
		"/api/me/services/"+ownedSvcID,
		serviceBody("a-draft", "curated-line", vehicleTypeID, "west-end", "east-end"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (%s)", rec.Code, rec.Body)
	}

	var got transit.Service
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.ID != ownedSvcID {
		t.Errorf("id: want it unchanged, got %q", got.ID)
	}
	if got.OwnerID == nil || *got.OwnerID != ownerAID {
		t.Errorf("owner: want it unchanged, got %v", got.OwnerID)
	}
}

// SPA-237 put a boarding-wait override on services but no write path for a
// seeded one; this CRUD is that path, so the three states its convention
// distinguishes have to survive the round trip.
func TestOwnedServiceCarriesTheBoardingWaitOverride(t *testing.T) {
	store := newFakeOwnedServiceStore()
	store.services[ownedSvcID] = transit.Service{
		ID: ownedSvcID, ScenarioID: ownedScrID, Name: "Local", OwnerID: ptrTo(ownerAID),
		BoardingWait: &transit.BoardingWaitOverride{Policy: transit.BoardingWaitHalfHeadway},
	}
	base := `"scenario_slug":"a-draft","route_slug":"a-line","vehicle_type_id":"` + vehicleTypeID + `",
		"name":"Local","stops":[{"station_slug":"west-end","sequence":1},{"station_slug":"east-end","sequence":2}]`

	t.Run("omitted leaves the stored override alone", func(t *testing.T) {
		rec := asServiceUser(t, handler.UpdateOwnedService(store, transit.DefaultBoardingWaitPolicy()),
			memberA, http.MethodPut, "/api/me/services/"+ownedSvcID, `{`+base+`}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status: want 200, got %d (%s)", rec.Code, rec.Body)
		}
		if got := store.services[ownedSvcID].BoardingWait; got == nil ||
			got.Policy != transit.BoardingWaitHalfHeadway {
			t.Errorf("override after an omitting update: want it kept, got %+v", got)
		}
	})

	t.Run("an object sets it", func(t *testing.T) {
		rec := asServiceUser(t, handler.UpdateOwnedService(store, transit.DefaultBoardingWaitPolicy()),
			memberA, http.MethodPut, "/api/me/services/"+ownedSvcID,
			`{`+base+`,"boarding_wait":{"policy":"fixed","secs":90}}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status: want 200, got %d (%s)", rec.Code, rec.Body)
		}
		got := store.services[ownedSvcID].BoardingWait
		if got == nil || got.Policy != transit.BoardingWaitFixed || got.Secs == nil || *got.Secs != 90 {
			t.Errorf("override after setting one: want fixed/90, got %+v", got)
		}
	})

	t.Run("an explicit null clears it back to inherit", func(t *testing.T) {
		rec := asServiceUser(t, handler.UpdateOwnedService(store, transit.DefaultBoardingWaitPolicy()),
			memberA, http.MethodPut, "/api/me/services/"+ownedSvcID,
			`{`+base+`,"boarding_wait":null}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status: want 200, got %d (%s)", rec.Code, rec.Body)
		}
		if got := store.services[ownedSvcID].BoardingWait; got != nil {
			t.Errorf("override after an explicit null: want nil (inherit), got %+v", got)
		}
	})

	t.Run("an unusable policy is a 422", func(t *testing.T) {
		rec := asServiceUser(t, handler.CreateOwnedService(store, transit.DefaultBoardingWaitPolicy()),
			memberA, http.MethodPost, "/api/me/services",
			`{`+base+`,"boarding_wait":{"policy":"whenever"}}`)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("status: want 422, got %d (%s)", rec.Code, rec.Body)
		}
	})
}

func TestOwnedServiceStorageFailureIsAnOpaque500(t *testing.T) {
	store := newFakeOwnedServiceStore()
	store.services[ownedSvcID] = transit.Service{ID: ownedSvcID, OwnerID: ptrTo(ownerAID)}
	store.failWith = fmt.Errorf("connection refused")

	rec := asServiceUser(t, handler.GetOwnedService(store, transit.DefaultBoardingWaitPolicy()),
		memberA, http.MethodGet, "/api/me/services/"+ownedSvcID, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "connection refused") {
		t.Error("the underlying error leaked into the response body")
	}
}
