package transit

import "testing"

func TestBoardingWaitPolicy_noneYieldsZero(t *testing.T) {
	windows := []FrequencyWindow{{HeadwayS: 3600}, {HeadwayS: 5400}}
	got, err := BoardingWaitPolicy{Kind: BoardingWaitNone}.WaitSecs(windows)
	if err != nil {
		t.Fatalf("WaitSecs: %v", err)
	}
	if got != 0 {
		t.Errorf("WaitSecs: want 0, got %d", got)
	}
}

func TestBoardingWaitPolicy_halfHeadwayMatchesCAHSR(t *testing.T) {
	// HSR Local peak/off-peak; Brightline West single window — values from the
	// seeded services.yaml and the ticket's preserved half_headway numbers.
	cases := []struct {
		name    string
		windows []FrequencyWindow
		want    int
	}{
		{"HSR Local", []FrequencyWindow{{HeadwayS: 3600}, {HeadwayS: 5400}}, 1800},
		{"Brightline West", []FrequencyWindow{{HeadwayS: 7200}}, 3600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BoardingWaitPolicy{Kind: BoardingWaitHalfHeadway}.WaitSecs(tc.windows)
			if err != nil {
				t.Fatalf("WaitSecs: %v", err)
			}
			if got != tc.want {
				t.Errorf("WaitSecs: want %d, got %d", tc.want, got)
			}
		})
	}
}

func TestBoardingWaitPolicy_fullHeadwayIsUnhalvedMin(t *testing.T) {
	cases := []struct {
		name    string
		windows []FrequencyWindow
		want    int
	}{
		{"HSR Local", []FrequencyWindow{{HeadwayS: 3600}, {HeadwayS: 5400}}, 3600},
		{"Brightline West", []FrequencyWindow{{HeadwayS: 7200}}, 7200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BoardingWaitPolicy{Kind: BoardingWaitFullHeadway}.WaitSecs(tc.windows)
			if err != nil {
				t.Fatalf("WaitSecs: %v", err)
			}
			if got != tc.want {
				t.Errorf("WaitSecs: want %d, got %d", tc.want, got)
			}
		})
	}
}

func TestBoardingWaitPolicy_fixedUsesExplicitSeconds(t *testing.T) {
	got, err := BoardingWaitPolicy{Kind: BoardingWaitFixed, FixedSecs: 120}.WaitSecs(
		[]FrequencyWindow{{HeadwayS: 3600}},
	)
	if err != nil {
		t.Fatalf("WaitSecs: %v", err)
	}
	if got != 120 {
		t.Errorf("WaitSecs: want 120, got %d", got)
	}
}

func TestParseBoardingWaitPolicy_rejectsNegativeFixed(t *testing.T) {
	_, err := ParseBoardingWaitPolicy("fixed", intPtr(-1))
	if err == nil {
		t.Fatal("expected error for negative fixed seconds")
	}
}

func TestParseBoardingWaitPolicy_rejectsUnknownKind(t *testing.T) {
	_, err := ParseBoardingWaitPolicy("average_headway", nil)
	if err == nil {
		t.Fatal("expected error for unrecognised policy")
	}
}

func TestParseBoardingWaitPolicy_fixedRequiresSeconds(t *testing.T) {
	_, err := ParseBoardingWaitPolicy("fixed", nil)
	if err == nil {
		t.Fatal("expected error when fixed has no companion seconds")
	}
}

func TestParseBoardingWaitPolicy_emptyDefaultsToNone(t *testing.T) {
	p, err := ParseBoardingWaitPolicy("", nil)
	if err != nil {
		t.Fatalf("ParseBoardingWaitPolicy: %v", err)
	}
	if p.Kind != BoardingWaitNone {
		t.Errorf("Kind: want %q, got %q", BoardingWaitNone, p.Kind)
	}
}

func intPtr(n int) *int { return &n }
