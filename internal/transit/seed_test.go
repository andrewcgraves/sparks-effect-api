package transit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

var _ SeedReconciler = (*fakeSeedSink)(nil)

type fakeSeedSink struct {
	scenarios    []Scenario
	vehicleTypes []VehicleType
	routes       []Route
	stations     []Station
	services     []Service
	links        [][2]string
	travelTimes  []TravelTimes
	listErr      error
	createErr    error
	updateErr    error
	updates      int
}

func (f *fakeSeedSink) ListCuratedScenarios(context.Context) ([]Scenario, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.scenarios, nil
}

func (f *fakeSeedSink) CreateScenario(_ context.Context, sc Scenario) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.scenarios = append(f.scenarios, sc)
	return nil
}

func (f *fakeSeedSink) CreateVehicleType(_ context.Context, vt VehicleType) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.vehicleTypes = append(f.vehicleTypes, vt)
	return nil
}

func (f *fakeSeedSink) CreateRoute(_ context.Context, r Route) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.routes = append(f.routes, r)
	return nil
}

func (f *fakeSeedSink) CreateStation(_ context.Context, st Station) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.stations = append(f.stations, st)
	return nil
}

func (f *fakeSeedSink) CreateService(_ context.Context, svc Service) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.services = append(f.services, svc)
	return nil
}

func (f *fakeSeedSink) AddServiceToScenario(_ context.Context, scenarioID, serviceID string) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.links = append(f.links, [2]string{scenarioID, serviceID})
	return nil
}

func (f *fakeSeedSink) UpsertTravelTimes(_ context.Context, tt TravelTimes) error {
	if f.createErr != nil {
		return f.createErr
	}
	for i, existing := range f.travelTimes {
		if existing.ScenarioSlug == tt.ScenarioSlug {
			f.travelTimes[i] = tt
			f.updates++
			return nil
		}
	}
	f.travelTimes = append(f.travelTimes, tt)
	return nil
}

func (f *fakeSeedSink) GetScenarioBySlug(_ context.Context, slug string) (Scenario, bool, error) {
	if f.listErr != nil {
		return Scenario{}, false, f.listErr
	}
	for _, sc := range f.scenarios {
		if sc.Slug == slug {
			return sc, true, nil
		}
	}
	return Scenario{}, false, nil
}

func (f *fakeSeedSink) ListVehicleTypes(context.Context) ([]VehicleType, error) {
	return f.vehicleTypes, nil
}

func (f *fakeSeedSink) ListRoutesByScenario(_ context.Context, scenarioID string) ([]Route, error) {
	var out []Route
	for _, r := range f.routes {
		if r.ScenarioID != nil && *r.ScenarioID == scenarioID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeSeedSink) ListStationsByScenario(_ context.Context, scenarioID string) ([]Station, error) {
	var out []Station
	for _, st := range f.stations {
		if st.ScenarioID == scenarioID {
			out = append(out, st)
		}
	}
	return out, nil
}

func (f *fakeSeedSink) ListServicesByScenario(_ context.Context, scenarioID string) ([]Service, error) {
	var out []Service
	for _, svc := range f.services {
		if svc.ScenarioID == scenarioID {
			out = append(out, svc)
		}
	}
	return out, nil
}

func (f *fakeSeedSink) GetTravelTimes(_ context.Context, scenarioSlug string) (TravelTimes, bool, error) {
	for _, tt := range f.travelTimes {
		if tt.ScenarioSlug == scenarioSlug {
			return tt, true, nil
		}
	}
	return TravelTimes{}, false, nil
}

func (f *fakeSeedSink) UpdateScenario(_ context.Context, sc Scenario) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	for i, existing := range f.scenarios {
		if existing.ID == sc.ID {
			f.scenarios[i] = sc
			f.updates++
			return nil
		}
	}
	return fmt.Errorf("no scenario %s", sc.ID)
}

func (f *fakeSeedSink) UpdateVehicleType(_ context.Context, vt VehicleType) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	for i, existing := range f.vehicleTypes {
		if existing.ID == vt.ID {
			f.vehicleTypes[i] = vt
			f.updates++
			return nil
		}
	}
	return fmt.Errorf("no vehicle type %s", vt.ID)
}

func (f *fakeSeedSink) UpdateRoute(_ context.Context, r Route) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	for i, existing := range f.routes {
		if existing.ID == r.ID {
			f.routes[i] = r
			f.updates++
			return nil
		}
	}
	return fmt.Errorf("no route %s", r.ID)
}

func (f *fakeSeedSink) UpdateStation(_ context.Context, st Station) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	for i, existing := range f.stations {
		if existing.ID == st.ID {
			f.stations[i] = st
			f.updates++
			return nil
		}
	}
	return fmt.Errorf("no station %s", st.ID)
}

func (f *fakeSeedSink) UpdateService(_ context.Context, svc Service) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	for i, existing := range f.services {
		if existing.ID == svc.ID {
			f.services[i] = svc
			f.updates++
			return nil
		}
	}
	return fmt.Errorf("no service %s", svc.ID)
}

func TestSeedFromEmbedded_writesThroughSink(t *testing.T) {
	sink := &fakeSeedSink{}
	if err := SeedFromEmbedded(context.Background(), sink); err != nil {
		t.Fatalf("SeedFromEmbedded: %v", err)
	}
	if len(sink.scenarios) != 1 || sink.scenarios[0].Slug != "ca-hsr" {
		t.Fatalf("scenarios = %+v, want one ca-hsr", sink.scenarios)
	}
	if len(sink.vehicleTypes) == 0 || len(sink.routes) == 0 || len(sink.stations) == 0 || len(sink.services) == 0 {
		t.Fatal("expected vehicle types, routes, stations, and services")
	}
	if len(sink.links) != len(sink.services) {
		t.Fatalf("service-scenario links = %d, want %d", len(sink.links), len(sink.services))
	}
	if len(sink.travelTimes) != 1 {
		t.Fatalf("travel times upserts = %d, want 1", len(sink.travelTimes))
	}
}

func TestSeedIfEmpty_skipsWhenCuratedScenariosExist(t *testing.T) {
	sink := &fakeSeedSink{scenarios: []Scenario{{ID: "existing", Slug: "already"}}}
	seeded, err := SeedIfEmpty(context.Background(), sink)
	if err != nil {
		t.Fatalf("SeedIfEmpty: %v", err)
	}
	if seeded {
		t.Fatal("expected no-op on a populated sink")
	}
	if len(sink.routes) != 0 {
		t.Fatal("no-op must not write")
	}
}

func TestSeedIfEmpty_seedsWhenEmpty(t *testing.T) {
	sink := &fakeSeedSink{}
	seeded, err := SeedIfEmpty(context.Background(), sink)
	if err != nil {
		t.Fatalf("SeedIfEmpty: %v", err)
	}
	if !seeded {
		t.Fatal("expected seed of an empty sink")
	}
	if len(sink.scenarios) != 1 || sink.scenarios[0].Slug != "ca-hsr" {
		t.Fatalf("scenarios = %+v, want one ca-hsr", sink.scenarios)
	}
}

func TestSeedIfEmpty_listError(t *testing.T) {
	sink := &fakeSeedSink{listErr: errors.New("db down")}
	_, err := SeedIfEmpty(context.Background(), sink)
	if err == nil || !strings.Contains(err.Error(), "existing scenarios") {
		t.Fatalf("got %v, want a list error", err)
	}
}

func TestSeedFromEmbedded_createError(t *testing.T) {
	sink := &fakeSeedSink{createErr: errors.New("db down")}
	err := SeedFromEmbedded(context.Background(), sink)
	if err == nil || !strings.Contains(err.Error(), "creating scenario") {
		t.Fatalf("got %v, want a create error", err)
	}
}

func TestValidateSegmentRoutes(t *testing.T) {
	routes := []Route{{ID: "route-1"}, {ID: "route-2"}}

	tests := []struct {
		name       string
		tt         TravelTimes
		wantErr    bool
		wantErrSeg string
	}{
		{
			name: "every segment names a route of the scenario",
			tt: TravelTimes{Segments: []SegmentTime{
				{FromSlug: "a", ToSlug: "b", RouteID: "route-1"},
				{FromSlug: "b", ToSlug: "c", RouteID: "route-2"},
			}},
			wantErr: false,
		},
		{
			name: "segment names a route the scenario does not have",
			tt: TravelTimes{Segments: []SegmentTime{
				{FromSlug: "a", ToSlug: "b", RouteID: "route-9"},
			}},
			wantErr: true,
		},
		{
			name: "segment names no route at all",
			tt: TravelTimes{Segments: []SegmentTime{
				{FromSlug: "a", ToSlug: "b"},
			}},
			wantErr: true,
		},
		{
			name:    "no segments",
			tt:      TravelTimes{},
			wantErr: false,
		},
		{
			name: "positive reverse duration is accepted",
			tt: TravelTimes{Segments: []SegmentTime{
				{FromSlug: "a", ToSlug: "b", RouteID: "route-1", RunSeconds: 600, ReverseRunSeconds: intPtr(400)},
			}},
			wantErr: false,
		},
		{
			name: "reverse duration equal to forward is accepted",
			tt: TravelTimes{Segments: []SegmentTime{
				{FromSlug: "a", ToSlug: "b", RouteID: "route-1", RunSeconds: 600, ReverseRunSeconds: intPtr(600)},
			}},
			wantErr: false,
		},
		{
			name: "zero reverse duration is rejected",
			tt: TravelTimes{Segments: []SegmentTime{
				{FromSlug: "a", ToSlug: "b", RouteID: "route-1", ReverseRunSeconds: intPtr(0)},
			}},
			wantErr:    true,
			wantErrSeg: "a→b",
		},
		{
			name: "negative reverse duration is rejected",
			tt: TravelTimes{Segments: []SegmentTime{
				{FromSlug: "a", ToSlug: "b", RouteID: "route-1", ReverseRunSeconds: intPtr(-1)},
			}},
			wantErr:    true,
			wantErrSeg: "a→b",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSegmentRoutes(routes, tc.tt)
			if tc.wantErr && err == nil {
				t.Error("want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("want no error, got %v", err)
			}
			if tc.wantErrSeg != "" && err != nil && !strings.Contains(err.Error(), tc.wantErrSeg) {
				t.Errorf("error should name segment %s, got: %v", tc.wantErrSeg, err)
			}
		})
	}
}
