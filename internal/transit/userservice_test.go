package transit_test

import (
	"strings"
	"testing"

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
	}{
		{"no name", func(s *transit.UserService) { s.Name = "" }, "name"},
		{"no route", func(s *transit.UserService) { s.RouteID = "" }, "route_id"},
		{"one stop", func(s *transit.UserService) { s.Stops = s.Stops[:1] }, "at least two stops"},
		{"no stops", func(s *transit.UserService) { s.Stops = nil }, "at least two stops"},
		{"unnamed stop", func(s *transit.UserService) { s.Stops[1].Name = "" }, "name"},
		{"lat out of range", func(s *transit.UserService) { s.Stops[0].Lat = 91 }, "lat"},
		{"lng out of range", func(s *transit.UserService) { s.Stops[0].Lng = -181 }, "lng"},
		{"zero max speed", func(s *transit.UserService) { s.Vehicle.MaxSpeedKMH = 0 }, "max_speed_kmh"},
		{"negative accel", func(s *transit.UserService) { s.Vehicle.AccelerationMS2 = -1 }, "acceleration_ms2"},
		{"zero decel", func(s *transit.UserService) { s.Vehicle.DecelerationMS2 = 0 }, "deceleration_ms2"},
		{"negative dwell", func(s *transit.UserService) { s.Vehicle.DwellS = -5 }, "dwell_s"},
		{"zero headway", func(s *transit.UserService) {
			s.FrequencyWindows[0].HeadwayS = 0
		}, "headway_s"},
		{"blank window time", func(s *transit.UserService) {
			s.FrequencyWindows[0].StartTime = ""
		}, "start_time"},
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
		})
	}
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
