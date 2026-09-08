package transit

import "testing"

func TestWaitSecsAcceptsBothServiceModels(t *testing.T) {
	windows := []FrequencyWindow{
		{StartTime: "06:00", EndTime: "10:00", HeadwayS: 1800},
		{StartTime: "10:00", EndTime: "16:00", HeadwayS: 600},
	}

	seeded := Service{ID: "svc-1", FrequencyWindows: windows}
	authored := UserService{ID: "us-1", FrequencyWindows: windows}

	halfHeadway := BoardingWaitPolicy{Kind: BoardingWaitHalfHeadway}

	const want = 300

	got, err := halfHeadway.WaitSecs(seeded.FrequencyWindows)
	if err != nil {
		t.Fatalf("seeded Service WaitSecs: %v", err)
	}
	if got != want {
		t.Errorf("seeded Service: got %d, want %d", got, want)
	}

	got, err = halfHeadway.WaitSecs(authored.FrequencyWindows)
	if err != nil {
		t.Fatalf("authored UserService WaitSecs: %v", err)
	}
	if got != want {
		t.Errorf("authored UserService: got %d, want %d", got, want)
	}
}
