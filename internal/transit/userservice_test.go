package transit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/fault"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func validUserService() transit.UserService {
	return transit.UserService{
		RouteID: "route-1",
		OwnerID: "user-1",
		Name:    "Bay Area Express",
		Vehicle: transit.VehicleParams{
			MaxSpeedKMH:     320,
			AccelerationMS2: 1.1,
			DecelerationMS2: 1.3,
			DwellS:          45,
		},
		Stops: []transit.ServiceStopPoint{
			{Name: "San Francisco", Lat: 37.7749, Lng: -122.4194, Seq: 0},
			{Name: "San Jose", Lat: 37.3382, Lng: -121.8863, Seq: 1},
		},
		FrequencyWindows: []transit.FrequencyWindow{
			{StartTime: "06:00", EndTime: "10:00", HeadwayS: 900},
		},
	}
}

func TestValidateAcceptsWellFormedService(t *testing.T) {
	if err := validUserService().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsBadServices(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*transit.UserService)
		want   string
		field  string
		rule   string
		index  *int
	}{
		{"no name", func(s *transit.UserService) { s.Name = "" }, "name", "name", fault.RuleRequired, nil},
		{"no route", func(s *transit.UserService) { s.RouteID = "" }, "route_id", "route_id", fault.RuleRequired, nil},
		{"one stop", func(s *transit.UserService) { s.Stops = s.Stops[:1] }, "at least two stops", "stops", fault.RuleMinCount, nil},
		{"no stops", func(s *transit.UserService) { s.Stops = nil }, "at least two stops", "stops", fault.RuleMinCount, nil},
		{"unnamed stop", func(s *transit.UserService) { s.Stops[1].Name = "" }, "name", "stops.name", fault.RuleRequired, fault.Index(1)},
		{"lat out of range", func(s *transit.UserService) { s.Stops[0].Lat = 91 }, "lat", "stops.lat", fault.RuleRange, fault.Index(0)},
		{"lng out of range", func(s *transit.UserService) { s.Stops[0].Lng = -181 }, "lng", "stops.lng", fault.RuleRange, fault.Index(0)},
		{"zero max speed", func(s *transit.UserService) { s.Vehicle.MaxSpeedKMH = 0 }, "max_speed_kmh", "vehicle.max_speed_kmh", fault.RulePositive, nil},
		{"negative accel", func(s *transit.UserService) { s.Vehicle.AccelerationMS2 = -1 }, "acceleration_ms2", "vehicle.acceleration_ms2", fault.RulePositive, nil},
		{"zero decel", func(s *transit.UserService) { s.Vehicle.DecelerationMS2 = 0 }, "deceleration_ms2", "vehicle.deceleration_ms2", fault.RulePositive, nil},
		{"negative dwell", func(s *transit.UserService) { s.Vehicle.DwellS = -5 }, "dwell_s", "vehicle.dwell_s", fault.RuleNonNegative, nil},
		{"zero headway", func(s *transit.UserService) {
			s.FrequencyWindows[0].HeadwayS = 0
		}, "headway_s", "frequency_windows.headway_s", fault.RulePositive, fault.Index(0)},
		{"blank window time", func(s *transit.UserService) {
			s.FrequencyWindows[0].StartTime = ""
		}, "start_time", "frequency_windows.start_time", fault.RuleRequired, fault.Index(0)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := validUserService()
			tc.mutate(&svc)
			err := svc.Validate()
			if err == nil {
				t.Fatalf("Validate: expected error mentioning %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate: got %q, want it to mention %q", err, tc.want)
			}
			got := mustValidationFaults(t, err)
			if len(got) != 1 {
				t.Fatalf("got %d faults, want 1: %+v", len(got), got)
			}
			assertFault(t, got[0], tc.field, tc.rule, tc.index)
		})
	}
}

func TestValidateReportsEveryBadStop(t *testing.T) {
	svc := validUserService()
	svc.Stops[0].Lat = 91
	svc.Stops[1].Lng = -181
	got := mustValidationFaults(t, svc.Validate())
	if len(got) != 2 {
		t.Fatalf("got %d faults, want 2: %+v", len(got), got)
	}
	assertFault(t, got[0], "stops.lat", fault.RuleRange, fault.Index(0))
	assertFault(t, got[1], "stops.lng", fault.RuleRange, fault.Index(1))
}

func TestValidateAcceptsNoFrequencyWindows(t *testing.T) {
	// A service with no declared windows is legal — headways are optional.
	svc := validUserService()
	svc.Stops = validUserService().Stops
	svc.FrequencyWindows = nil
	if err := svc.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestNormalizeStopsRenumbersInOrder(t *testing.T) {
	// Ordering is the contract: stops come back in the order given, with a
	// dense 0..n-1 sequence, regardless of what seq the client sent.
	svc := validUserService()
	svc.Stops = []transit.ServiceStopPoint{
		{Name: "A", Lat: 1, Lng: 1, Seq: 7},
		{Name: "B", Lat: 2, Lng: 2, Seq: 7},
		{Name: "C", Lat: 3, Lng: 3, Seq: 2},
	}
	svc.NormalizeStops()

	for i, want := range []string{"A", "B", "C"} {
		if svc.Stops[i].Name != want || svc.Stops[i].Seq != i {
			t.Fatalf("stop %d: got %s/seq=%d, want %s/seq=%d",
				i, svc.Stops[i].Name, svc.Stops[i].Seq, want, i)
		}
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Bay Area Express", "bay-area-express"},
		{"  Caltrain   Local  ", "caltrain-local"},
		{"SF→SJ (Express!)", "sf-sj-express"},
		{"already-a-slug", "already-a-slug"},
		{"Route 99", "route-99"},
		{"!!!", "service"},
		{"", "service"},
	}
	for _, tc := range tests {
		if got := transit.Slugify(tc.in); got != tc.want {
			t.Errorf("Slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSlugifyTruncatesWithoutTrailingDash(t *testing.T) {
	got := transit.Slugify(strings.Repeat("ab ", 60))
	if len(got) > 80 {
		t.Fatalf("slug too long: %d chars", len(got))
	}
	if strings.HasSuffix(got, "-") {
		t.Fatalf("slug has trailing dash: %q", got)
	}
}

func mustValidationFaults(t *testing.T, err error) fault.ValidationFaults {
	t.Helper()
	var faults fault.ValidationFaults
	if !errors.As(err, &faults) {
		t.Fatalf("error %v (%T) is not fault.ValidationFaults", err, err)
	}
	return faults
}

func assertFault(t *testing.T, got fault.ValidationFault, field, rule string, index *int) {
	t.Helper()
	if got.Field != field {
		t.Errorf("field = %q, want %q", got.Field, field)
	}
	if got.Rule != rule {
		t.Errorf("rule = %q, want %q", got.Rule, rule)
	}
	switch {
	case index == nil && got.Index != nil:
		t.Errorf("index = %d, want nil", *got.Index)
	case index != nil && got.Index == nil:
		t.Errorf("index = nil, want %d", *index)
	case index != nil && got.Index != nil && *index != *got.Index:
		t.Errorf("index = %d, want %d", *got.Index, *index)
	}
	if got.Message == "" {
		t.Error("message is empty")
	}
}
