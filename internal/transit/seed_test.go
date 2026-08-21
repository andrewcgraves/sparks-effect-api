package transit

import (
	"strings"
	"testing"
)

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
