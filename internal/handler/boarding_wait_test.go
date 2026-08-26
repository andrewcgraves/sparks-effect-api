package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// The four read endpoints that report the boarding wait a compile would charge
// (SPA-236). They are covered together because the guarantee is the same on all
// of them — a client reads the resolved wait rather than re-deriving
// min(headway)/2 — and it is the kind of guarantee that gets added to one
// handler and forgotten on the other three.
//
// The seeded transit.Service and the user-authored transit.UserService are both
// represented: they carry the same two response-only fields but reach them
// through different handlers.
type boardingWaitRead struct {
	name string
	// peakHeadwayS is the smallest headway across the fixture service's
	// windows, stated here rather than read back off the response so the
	// expectations are not derived from the value under test.
	peakHeadwayS int
	// read serves the endpoint under policy and returns the fixture service as
	// the client sees it — a decoded object rather than a typed struct, so a
	// test can tell an absent field from a zero one.
	read func(t *testing.T, policy transit.BoardingWaitPolicy) map[string]any
}

func boardingWaitReads() []boardingWaitRead {
	return []boardingWaitRead{
		{
			name:         "GET /api/services/{slug}",
			peakHeadwayS: 1800,
			read: func(t *testing.T, policy transit.BoardingWaitPolicy) map[string]any {
				t.Helper()
				store := newFakeServiceStore()
				store.services["svc-bw"] = boardingWaitUserService()

				req := httptest.NewRequest(http.MethodGet, "/api/services/night-owl", nil)
				req.SetPathValue("slug", "night-owl")
				req = req.WithContext(auth.WithUser(req.Context(), svcOwner))
				rec := httptest.NewRecorder()
				handler.GetService(store, policy).ServeHTTP(rec, req)
				return decodeObject(t, rec)
			},
		},
		{
			name:         "GET /api/services",
			peakHeadwayS: 1800,
			read: func(t *testing.T, policy transit.BoardingWaitPolicy) map[string]any {
				t.Helper()
				store := newFakeServiceStore()
				store.services["svc-bw"] = boardingWaitUserService()

				rec := getAs(t, handler.MyUserServices(store, policy), "/api/services", svcOwner)
				return decodeNamed(t, rec, "Night Owl")
			},
		},
		{
			name:         "GET /api/me/services",
			peakHeadwayS: 1800,
			read: func(t *testing.T, policy transit.BoardingWaitPolicy) map[string]any {
				t.Helper()
				store := &fakeOwnerStore{
					services: map[string][]transit.Service{
						svcOwner.ID: {boardingWaitSeededService()},
					},
				}

				rec := getAs(t, handler.MyServices(store, policy), "/api/me/services", svcOwner)
				return decodeNamed(t, rec, "Night Owl")
			},
		},
		{
			// The one case backed by real seed data rather than a fixture: HSR
			// Local runs on a 3600 s peak headway, so half_headway resolves to
			// the 1800 s the client would otherwise have had to work out.
			name:         "GET /api/scenarios/{slug}/services",
			peakHeadwayS: 3600,
			read: func(t *testing.T, policy transit.BoardingWaitPolicy) map[string]any {
				t.Helper()
				store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
				if err != nil {
					t.Fatalf("NewStore: %v", err)
				}

				req := httptest.NewRequest(http.MethodGet, "/api/scenarios/ca-hsr/services", nil)
				req.SetPathValue("slug", "ca-hsr")
				rec := httptest.NewRecorder()
				handler.ScenarioServices(store, policy).ServeHTTP(rec, req)
				return decodeNamed(t, rec, "HSR Local")
			},
		},
	}
}

func boardingWaitUserService() transit.UserService {
	return transit.UserService{
		ID: "svc-bw", Slug: "night-owl", OwnerID: svcOwner.ID, RouteID: "route-1", Name: "Night Owl",
		FrequencyWindows: []transit.FrequencyWindow{
			{StartTime: "06:00", EndTime: "10:00", HeadwayS: 1800},
			{StartTime: "10:00", EndTime: "20:00", HeadwayS: 2700},
		},
	}
}

func boardingWaitSeededService() transit.Service {
	owner := svcOwner.ID
	return transit.Service{
		ID: "svc-bw", RouteID: "route-1", Name: "Night Owl", OwnerID: &owner,
		FrequencyWindows: []transit.FrequencyWindow{
			{StartTime: "06:00", EndTime: "10:00", HeadwayS: 1800},
			{StartTime: "10:00", EndTime: "20:00", HeadwayS: 2700},
		},
	}
}

func TestServiceReadsExposeTheResolvedBoardingWait(t *testing.T) {
	for _, endpoint := range boardingWaitReads() {
		t.Run(endpoint.name, func(t *testing.T) {
			policies := []struct {
				policy   transit.BoardingWaitPolicy
				wantKind transit.BoardingWaitKind
				wantSecs int
			}{
				{transit.DefaultBoardingWaitPolicy(), transit.BoardingWaitNone, 0},
				{transit.BoardingWaitPolicy{Kind: transit.BoardingWaitHalfHeadway}, transit.BoardingWaitHalfHeadway, endpoint.peakHeadwayS / 2},
				{transit.BoardingWaitPolicy{Kind: transit.BoardingWaitFullHeadway}, transit.BoardingWaitFullHeadway, endpoint.peakHeadwayS},
				{transit.BoardingWaitPolicy{Kind: transit.BoardingWaitFixed, FixedSecs: 240}, transit.BoardingWaitFixed, 240},
			}
			for _, p := range policies {
				t.Run(string(p.wantKind), func(t *testing.T) {
					assertBoardingWait(t, endpoint.read(t, p.policy), p.wantKind, p.wantSecs)
				})
			}
		})
	}
}

// A policy the handler cannot resolve must not answer with a bare zero.
// boarding_wait_secs is always serialised and boarding_wait_policy is omitempty,
// so leaving both fields untouched publishes a wait of 0 s under no policy at
// all — indistinguishable from a compiled none. The two fall back together
// instead.
func TestServiceReadsReportUnresolvablePolicyAsNoneNotABareZero(t *testing.T) {
	unresolvable := transit.BoardingWaitPolicy{Kind: "not_a_policy"}

	for _, endpoint := range boardingWaitReads() {
		t.Run(endpoint.name, func(t *testing.T) {
			svc := endpoint.read(t, unresolvable)
			if _, ok := svc["boarding_wait_policy"]; !ok {
				t.Error("boarding_wait_policy omitted; a zero wait with no policy reads as a compiled none")
			}
			assertBoardingWait(t, svc, transit.BoardingWaitNone, 0)
		})
	}
}

func assertBoardingWait(t *testing.T, svc map[string]any, wantKind transit.BoardingWaitKind, wantSecs int) {
	t.Helper()
	assertBoardingWaitFrom(t, svc, wantKind, wantSecs, transit.BoardingWaitSourceGlobal)
}

func assertBoardingWaitFrom(t *testing.T, svc map[string]any, wantKind transit.BoardingWaitKind, wantSecs int, wantSource string) {
	t.Helper()
	if got := svc["boarding_wait_policy"]; got != string(wantKind) {
		t.Errorf("boarding_wait_policy = %v, want %q", got, wantKind)
	}
	if got := svc["boarding_wait_secs"]; got != float64(wantSecs) {
		t.Errorf("boarding_wait_secs = %v, want %d", got, wantSecs)
	}
	if got := svc["boarding_wait_source"]; got != wantSource {
		t.Errorf("boarding_wait_source = %v, want %q", got, wantSource)
	}
}

func decodeObject(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var obj map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	return obj
}

// decodeNamed picks one service out of a list response by name, so a list
// endpoint reports through the same shape as a single-service one.
func decodeNamed(t *testing.T, rec *httptest.ResponseRecorder, name string) map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	for _, svc := range list {
		if svc["name"] == name {
			return svc
		}
	}
	t.Fatalf("no service named %q in %s", name, rec.Body.String())
	return nil
}

func TestCreateService_acceptsBoardingWaitOverride(t *testing.T) {
	store := newFakeServiceStore()
	body := `{
		"route_slug": "sf-sj",
		"name": "Night Owl",
		"vehicle": {"max_speed_kmh": 320, "acceleration_ms2": 1.1, "deceleration_ms2": 1.3, "dwell_s": 45},
		"stops": [
			{"name": "San Francisco", "lat": 37.7749, "lng": -122.4194},
			{"name": "San Jose", "lat": 37.3382, "lng": -121.8863}
		],
		"frequency_windows": [{"start_time": "06:00", "end_time": "10:00", "headway_s": 1800}],
		"boarding_wait": {"policy": "fixed", "secs": 90}
	}`
	rec := serveAs(t, store, svcOwner, http.MethodPost, "/api/services", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assertBoardingWaitFrom(t, created, transit.BoardingWaitFixed, 90, transit.BoardingWaitSourceService)
	if created["boarding_wait"] == nil {
		t.Fatal("boarding_wait omitted on create; the stored override should round-trip")
	}
}

func TestUpdateService_omitLeavesOverrideAndNullClearsIt(t *testing.T) {
	store := newFakeServiceStore()
	secs := 90
	seed := seedService(store, "svc-bw", "night-owl", svcOwner.ID)
	seed.FrequencyWindows = []transit.FrequencyWindow{{StartTime: "06:00", EndTime: "10:00", HeadwayS: 1800}}
	seed.BoardingWait = &transit.BoardingWaitOverride{Policy: transit.BoardingWaitFixed, Secs: &secs}
	store.services[seed.ID] = seed

	omitBody := `{
		"route_slug": "diagonal",
		"name": "Night Owl",
		"vehicle": {"max_speed_kmh": 200, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 30},
		"stops": [{"name": "A", "lat": 1, "lng": 1}, {"name": "B", "lat": 2, "lng": 2}],
		"frequency_windows": [{"start_time": "06:00", "end_time": "10:00", "headway_s": 1800}]
	}`
	rec := serveAs(t, store, svcOwner, http.MethodPut, "/api/services/night-owl", omitBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("omit update: got %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got := decodeObject(t, rec)
	assertBoardingWaitFrom(t, got, transit.BoardingWaitFixed, 90, transit.BoardingWaitSourceService)

	nullBody := `{
		"route_slug": "diagonal",
		"name": "Night Owl",
		"vehicle": {"max_speed_kmh": 200, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 30},
		"stops": [{"name": "A", "lat": 1, "lng": 1}, {"name": "B", "lat": 2, "lng": 2}],
		"frequency_windows": [{"start_time": "06:00", "end_time": "10:00", "headway_s": 1800}],
		"boarding_wait": null
	}`
	rec = serveAs(t, store, svcOwner, http.MethodPut, "/api/services/night-owl", nullBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("null update: got %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got = decodeObject(t, rec)
	assertBoardingWaitFrom(t, got, transit.BoardingWaitNone, 0, transit.BoardingWaitSourceGlobal)
	if _, ok := got["boarding_wait"]; ok {
		t.Errorf("boarding_wait still present after clear: %v", got["boarding_wait"])
	}
}

func TestCreateService_rejectsInvalidBoardingWait(t *testing.T) {
	tests := []struct {
		name string
		wait string
	}{
		{"unknown policy", `{"policy":"average_headway"}`},
		{"fixed without secs", `{"policy":"fixed"}`},
		{"negative fixed", `{"policy":"fixed","secs":-1}`},
	}
	base := `{
		"route_slug": "sf-sj",
		"name": "Night Owl",
		"vehicle": {"max_speed_kmh": 320, "acceleration_ms2": 1.1, "deceleration_ms2": 1.3, "dwell_s": 45},
		"stops": [
			{"name": "San Francisco", "lat": 37.7749, "lng": -122.4194},
			{"name": "San Jose", "lat": 37.3382, "lng": -121.8863}
		],
		"boarding_wait": %s
	}`
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := serveAs(t, newFakeServiceStore(), svcOwner, http.MethodPost, "/api/services", fmt.Sprintf(base, tc.wait))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("got %d, want 422 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestCreateUserScenario_acceptsBoardingWaitOverride(t *testing.T) {
	store := newFakeScenarioStore()
	body := `{"name": "Weekend Getaway", "description": "Fri-Sun", "service_ids": ["svc-1", "svc-2"],
		"boarding_wait": {"policy": "half_headway"}}`
	rec := scnServeAs(t, store, scnOwner, http.MethodPost, "/api/user-scenarios", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["boarding_wait_policy"] != string(transit.BoardingWaitHalfHeadway) {
		t.Errorf("boarding_wait_policy = %v, want half_headway", got["boarding_wait_policy"])
	}
	if got["boarding_wait_source"] != transit.BoardingWaitSourceScenario {
		t.Errorf("boarding_wait_source = %v, want scenario", got["boarding_wait_source"])
	}
	if got["boarding_wait"] == nil {
		t.Fatal("boarding_wait omitted on create; the stored override should round-trip")
	}
}

func TestCreateUserScenario_rejectsInvalidBoardingWait(t *testing.T) {
	tests := []struct {
		name string
		wait string
	}{
		{"unknown policy", `{"policy":"average_headway"}`},
		{"fixed without secs", `{"policy":"fixed"}`},
		{"negative fixed", `{"policy":"fixed","secs":-1}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"name": "Weekend Getaway", "service_ids": ["svc-1"], "boarding_wait": %s}`, tc.wait)
			rec := scnServeAs(t, newFakeScenarioStore(), scnOwner, http.MethodPost, "/api/user-scenarios", body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("got %d, want 422 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestUpdateUserScenario_omitLeavesOverrideAndNullClearsIt(t *testing.T) {
	store := newFakeScenarioStore()
	sc := seedScenarioRow(store, "scn-1", "weekend-getaway", scnOwner.ID, []string{"svc-1"})
	sc.BoardingWait = &transit.BoardingWaitOverride{Policy: transit.BoardingWaitHalfHeadway}
	store.scenarios[sc.ID] = sc

	rec := scnServeAs(t, store, scnOwner, http.MethodPut, "/api/user-scenarios/weekend-getaway",
		`{"name": "Weekend Getaway", "service_ids": ["svc-1"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("omit update: got %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got := decodeObject(t, rec)
	if got["boarding_wait_source"] != transit.BoardingWaitSourceScenario {
		t.Errorf("omit: source = %v, want scenario", got["boarding_wait_source"])
	}

	rec = scnServeAs(t, store, scnOwner, http.MethodPut, "/api/user-scenarios/weekend-getaway",
		`{"name": "Weekend Getaway", "service_ids": ["svc-1"], "boarding_wait": null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("null update: got %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got = decodeObject(t, rec)
	if got["boarding_wait_source"] != transit.BoardingWaitSourceGlobal {
		t.Errorf("null: source = %v, want global", got["boarding_wait_source"])
	}
}
