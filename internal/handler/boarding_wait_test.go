package handler_test

import (
	"encoding/json"
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
	if got := svc["boarding_wait_policy"]; got != string(wantKind) {
		t.Errorf("boarding_wait_policy = %v, want %q", got, wantKind)
	}
	if got := svc["boarding_wait_secs"]; got != float64(wantSecs) {
		t.Errorf("boarding_wait_secs = %v, want %d", got, wantSecs)
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
