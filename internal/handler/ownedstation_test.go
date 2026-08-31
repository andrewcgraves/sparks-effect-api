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

// fakeOwnedStationStore is an in-memory handler.OwnedStationStore and
// handler.OwnedTravelTimesStore — both surfaces read the same scenario's
// children, so one fake serves both rather than two that must agree.
type fakeOwnedStationStore struct {
	*fakeOwnedScenarioStore

	stations   map[string][]transit.Station // by scenario id
	routes     map[string][]transit.Route   // by scenario id
	travel     map[string]transit.TravelTimes
	dependents map[string]int // station id -> service stops
	storeErr   error
}

func newFakeOwnedStationStore() *fakeOwnedStationStore {
	scenarios := newFakeOwnedScenarioStore()
	scenarios.scenarios["a-draft"] = transit.Scenario{
		ID: ownedScrID, Slug: "a-draft", Name: "A Draft", OwnerID: ptrTo(ownerAID),
	}
	return &fakeOwnedStationStore{
		fakeOwnedScenarioStore: scenarios,
		stations:               map[string][]transit.Station{},
		routes: map[string][]transit.Route{
			ownedScrID: {{ID: "rt-1", Slug: "a-line", OwnerID: ptrTo(ownerAID)}},
		},
		travel:     map[string]transit.TravelTimes{},
		dependents: map[string]int{},
	}
}

func (f *fakeOwnedStationStore) CreateStation(_ context.Context, st transit.Station) error {
	if f.storeErr != nil {
		return f.storeErr
	}
	f.stations[st.ScenarioID] = append(f.stations[st.ScenarioID], st)
	return nil
}

func (f *fakeOwnedStationStore) GetStationBySlug(_ context.Context, scenarioID, slug string) (transit.Station, bool, error) {
	if f.storeErr != nil {
		return transit.Station{}, false, f.storeErr
	}
	for _, st := range f.stations[scenarioID] {
		if st.Slug == slug {
			return st, true, nil
		}
	}
	return transit.Station{}, false, nil
}

func (f *fakeOwnedStationStore) UpdateStation(_ context.Context, st transit.Station) error {
	if f.storeErr != nil {
		return f.storeErr
	}
	for i, existing := range f.stations[st.ScenarioID] {
		if existing.ID == st.ID {
			f.stations[st.ScenarioID][i] = st
			return nil
		}
	}
	return fmt.Errorf("no station with id %q", st.ID)
}

func (f *fakeOwnedStationStore) DeleteStation(_ context.Context, id string) error {
	if f.storeErr != nil {
		return f.storeErr
	}
	for scenarioID, list := range f.stations {
		for i, st := range list {
			if st.ID == id {
				f.stations[scenarioID] = append(list[:i], list[i+1:]...)
				return nil
			}
		}
	}
	return fmt.Errorf("no station with id %q", id)
}

func (f *fakeOwnedStationStore) CountStationDependents(_ context.Context, id string) (int, error) {
	if f.storeErr != nil {
		return 0, f.storeErr
	}
	return f.dependents[id], nil
}

func (f *fakeOwnedStationStore) ListStationsByScenario(_ context.Context, scenarioID string) ([]transit.Station, error) {
	if f.storeErr != nil {
		return nil, f.storeErr
	}
	return f.stations[scenarioID], nil
}

func (f *fakeOwnedStationStore) ListRoutesByScenario(_ context.Context, scenarioID string) ([]transit.Route, error) {
	if f.storeErr != nil {
		return nil, f.storeErr
	}
	return f.routes[scenarioID], nil
}

func (f *fakeOwnedStationStore) UpsertTravelTimes(_ context.Context, tt transit.TravelTimes) error {
	if f.storeErr != nil {
		return f.storeErr
	}
	f.travel[tt.ScenarioSlug] = tt
	return nil
}

func (f *fakeOwnedStationStore) GetTravelTimes(_ context.Context, slug string) (transit.TravelTimes, bool, error) {
	if f.storeErr != nil {
		return transit.TravelTimes{}, false, f.storeErr
	}
	tt, ok := f.travel[slug]
	return tt, ok, nil
}

// asStationUser binds both wildcards the station paths use.
func asStationUser(t *testing.T, h http.HandlerFunc, user transit.User,
	method, scenarioSlug, stationSlug, body string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/api/me/scenarios/" + scenarioSlug + "/stations"
	if stationSlug != "" {
		target += "/" + stationSlug
	}
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.SetPathValue("slug", scenarioSlug)
	req.SetPathValue("stationSlug", stationSlug)
	req = req.WithContext(auth.WithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestCreateOwnedStationInheritsTheScenariosOwner(t *testing.T) {
	store := newFakeOwnedStationStore()

	rec := asStationUser(t, handler.CreateOwnedStation(store), memberA, http.MethodPost,
		"a-draft", "", `{"name":"West End","lat":37.0,"lng":-121.9,"platform_height":"high"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d (%s)", rec.Code, rec.Body)
	}
	var got transit.Station
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	// The uniformity invariant: a station's owner is its scenario's owner, not
	// something the request supplied.
	if got.OwnerID == nil || *got.OwnerID != ownerAID {
		t.Errorf("owner: want %q, got %v", ownerAID, got.OwnerID)
	}
	if got.ScenarioID != ownedScrID {
		t.Errorf("scenario: want %q, got %q", ownedScrID, got.ScenarioID)
	}
	if got.Slug != "west-end" {
		t.Errorf("slug: want west-end, got %q", got.Slug)
	}
	// Stored as GeoJSON [lng, lat] even though it is authored as lat/lng.
	if len(got.Location.Coordinates) != 2 ||
		got.Location.Coordinates[0] != -121.9 || got.Location.Coordinates[1] != 37.0 {
		t.Errorf("location: want [-121.9, 37], got %+v", got.Location.Coordinates)
	}
}

// Nobody may add stations to a scenario they do not own, curated or otherwise.
func TestCreateOwnedStationRefusesAScenarioTheCallerDoesNotOwn(t *testing.T) {
	for _, tc := range []struct {
		name string
		user transit.User
		slug string
		want int
	}{
		{"a member on their own", memberA, "a-draft", http.StatusCreated},
		{"a member on the curated baseline", memberA, "ca-hsr", http.StatusNotFound},
		{"a stranger on someone else's", memberB, "a-draft", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeOwnedStationStore()
			rec := asStationUser(t, handler.CreateOwnedStation(store), tc.user, http.MethodPost,
				tc.slug, "", `{"name":"West End","lat":37.0,"lng":-121.9}`)
			if rec.Code != tc.want {
				t.Errorf("status: want %d, got %d (%s)", tc.want, rec.Code, rec.Body)
			}
		})
	}
}

// jsonb will happily store a longitude of 500, so the bounds are checked here.
func TestCreateOwnedStationValidatesCoordinates(t *testing.T) {
	for _, tc := range []struct{ name, body, wantIn string }{
		{"no name", `{"name":"  ","lat":37,"lng":-121.9}`, "name is required"},
		{"latitude out of range", `{"name":"X","lat":91,"lng":-121.9}`, "latitude"},
		{"longitude out of range", `{"name":"X","lat":37,"lng":500}`, "longitude"},
		{"half a routing location", `{"name":"X","lat":37,"lng":-121.9,"routing_lat":37}`, "together"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeOwnedStationStore()
			rec := asStationUser(t, handler.CreateOwnedStation(store), memberA,
				http.MethodPost, "a-draft", "", tc.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status: want 422, got %d (%s)", rec.Code, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tc.wantIn) {
				t.Errorf("body %s: want it to mention %q", rec.Body, tc.wantIn)
			}
		})
	}
}

// Travel-time segments address stations by slug, so a rename must not re-slug
// or every segment naming the station is orphaned.
func TestUpdateOwnedStationKeepsTheSlug(t *testing.T) {
	store := newFakeOwnedStationStore()
	store.stations[ownedScrID] = []transit.Station{
		{ID: "st-1", ScenarioID: ownedScrID, Slug: "west-end", Name: "West End", OwnerID: ptrTo(ownerAID)},
	}

	rec := asStationUser(t, handler.UpdateOwnedStation(store), memberA, http.MethodPut,
		"a-draft", "west-end", `{"name":"Completely Renamed","lat":37.5,"lng":-122.0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (%s)", rec.Code, rec.Body)
	}

	var got transit.Station
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Slug != "west-end" {
		t.Errorf("slug: want it unchanged, got %q", got.Slug)
	}
	if got.Name != "Completely Renamed" {
		t.Errorf("name: want it updated, got %q", got.Name)
	}
}

// service_stops.station_id is RESTRICT, so the pre-check is what turns an
// opaque 500 into a 409 naming what is in the way.
func TestDeleteOwnedStationRefusesWhileAServiceStopsThere(t *testing.T) {
	store := newFakeOwnedStationStore()
	store.stations[ownedScrID] = []transit.Station{
		{ID: "st-1", ScenarioID: ownedScrID, Slug: "west-end", OwnerID: ptrTo(ownerAID)},
	}
	store.dependents["st-1"] = 3

	rec := asStationUser(t, handler.DeleteOwnedStation(store), memberA,
		http.MethodDelete, "a-draft", "west-end", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status: want 409, got %d (%s)", rec.Code, rec.Body)
	}
	if code := errorCodeOf(t, rec.Body.Bytes()); code != "station_in_use" {
		t.Errorf("code: want station_in_use, got %q", code)
	}
}

func TestDeleteOwnedStationSucceedsWhenNothingStopsThere(t *testing.T) {
	store := newFakeOwnedStationStore()
	store.stations[ownedScrID] = []transit.Station{
		{ID: "st-1", ScenarioID: ownedScrID, Slug: "west-end", OwnerID: ptrTo(ownerAID)},
	}

	rec := asStationUser(t, handler.DeleteOwnedStation(store), memberA,
		http.MethodDelete, "a-draft", "west-end", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: want 204, got %d (%s)", rec.Code, rec.Body)
	}
	if len(store.stations[ownedScrID]) != 0 {
		t.Error("the station survived its delete")
	}
}

// --- Travel times ---

func TestReplaceOwnedTravelTimesResolvesSlugsToIds(t *testing.T) {
	store := newFakeOwnedStationStore()
	store.stations[ownedScrID] = []transit.Station{
		{ID: "st-1", ScenarioID: ownedScrID, Slug: "west-end", OwnerID: ptrTo(ownerAID)},
		{ID: "st-2", ScenarioID: ownedScrID, Slug: "east-end", OwnerID: ptrTo(ownerAID)},
	}

	body := `{"provenance":"authored","source":"me","segments":[
		{"from":"west-end","to":"east-end","run_seconds":600,"route_slug":"a-line"}]}`
	rec := asTravelTimesUser(t, handler.ReplaceOwnedTravelTimes(store), memberA,
		http.MethodPut, "a-draft", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (%s)", rec.Code, rec.Body)
	}

	var got transit.TravelTimes
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Segments) != 1 {
		t.Fatalf("segments: want 1, got %+v", got.Segments)
	}
	// The route slug is resolved to its id, so a client can never supply one.
	if got.Segments[0].RouteID != "rt-1" {
		t.Errorf("route id: want rt-1, got %q", got.Segments[0].RouteID)
	}
	if got.ScenarioSlug != "a-draft" {
		t.Errorf("scenario: want it taken from the path, got %q", got.ScenarioSlug)
	}
}

// A bad segment is a 422 naming it, not a scenario that stores fine and then
// fails to compile with a message about a stop "not on any segment path".
func TestReplaceOwnedTravelTimesRejectsBadSegments(t *testing.T) {
	seed := func() *fakeOwnedStationStore {
		store := newFakeOwnedStationStore()
		store.stations[ownedScrID] = []transit.Station{
			{ID: "st-1", ScenarioID: ownedScrID, Slug: "west-end", OwnerID: ptrTo(ownerAID)},
			{ID: "st-2", ScenarioID: ownedScrID, Slug: "east-end", OwnerID: ptrTo(ownerAID)},
		}
		return store
	}

	for _, tc := range []struct{ name, segment, wantIn string }{
		{"an unknown station", `{"from":"west-end","to":"nowhere","run_seconds":600,"route_slug":"a-line"}`, "nowhere"},
		{"a self-loop", `{"from":"west-end","to":"west-end","run_seconds":600,"route_slug":"a-line"}`, "same station"},
		{"a zero run time", `{"from":"west-end","to":"east-end","run_seconds":0,"route_slug":"a-line"}`, "run_seconds"},
		{"a route from elsewhere", `{"from":"west-end","to":"east-end","run_seconds":600,"route_slug":"other"}`, "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := asTravelTimesUser(t, handler.ReplaceOwnedTravelTimes(seed()), memberA,
				http.MethodPut, "a-draft", `{"segments":[`+tc.segment+`]}`)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status: want 422, got %d (%s)", rec.Code, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tc.wantIn) {
				t.Errorf("body %s: want it to name %q", rec.Body, tc.wantIn)
			}
		})
	}
}

// A scenario with no segment times yet has an empty set, which the editor
// renders — not a 404, which it would have to special-case.
func TestGetOwnedTravelTimesAnswersEmptyBeforeAnyAreWritten(t *testing.T) {
	store := newFakeOwnedStationStore()

	rec := asTravelTimesUser(t, handler.GetOwnedTravelTimes(store), memberA,
		http.MethodGet, "a-draft", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (%s)", rec.Code, rec.Body)
	}
	var got transit.TravelTimes
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Segments == nil {
		t.Error("segments: want [] on the wire, got null")
	}
}

func TestOwnedTravelTimesAreScopedToTheirScenariosOwner(t *testing.T) {
	store := newFakeOwnedStationStore()

	rec := asTravelTimesUser(t, handler.GetOwnedTravelTimes(store), memberB,
		http.MethodGet, "a-draft", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("a stranger reading someone's travel times: want 404, got %d", rec.Code)
	}
}

func asTravelTimesUser(t *testing.T, h http.HandlerFunc, user transit.User,
	method, scenarioSlug, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method,
		"/api/me/scenarios/"+scenarioSlug+"/travel-times", strings.NewReader(body))
	req.SetPathValue("slug", scenarioSlug)
	req = req.WithContext(auth.WithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}
