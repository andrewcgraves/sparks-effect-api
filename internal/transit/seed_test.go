package transit

import "testing"

func TestValidateSegmentRoutes(t *testing.T) {
	routes := []Route{{ID: "route-1"}, {ID: "route-2"}}

	tests := []struct {
		name    string
		tt      TravelTimes
		wantErr bool
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
		})
	}
}
