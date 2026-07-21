package transit

import "testing"

// TestBestHeadwayOver2AcceptsBothServiceModels is the point of sharing
// FrequencyWindow: the seeded Service and the user-authored UserService express
// headways with one type, so compiler helpers run against either without a
// parallel implementation. Before the types were unified this did not compile.
func TestBestHeadwayOver2AcceptsBothServiceModels(t *testing.T) {
	windows := []FrequencyWindow{
		{StartTime: "06:00", EndTime: "10:00", HeadwayS: 1800},
		{StartTime: "10:00", EndTime: "16:00", HeadwayS: 600},
	}

	seeded := Service{ID: "svc-1", FrequencyWindows: windows}
	authored := UserService{ID: "us-1", FrequencyWindows: windows}

	// Shortest headway (600s) halved.
	const want = 300
	if got := bestHeadwayOver2(seeded.FrequencyWindows); got != want {
		t.Errorf("seeded Service: got %d, want %d", got, want)
	}
	if got := bestHeadwayOver2(authored.FrequencyWindows); got != want {
		t.Errorf("authored UserService: got %d, want %d", got, want)
	}
}
