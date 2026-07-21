package transit_test

import (
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func validUserScenario() transit.UserScenario {
	return transit.UserScenario{
		OwnerID:    "owner-1",
		Name:       "Weekend Getaway",
		ServiceIDs: []string{"svc-1", "svc-2"},
	}
}

func TestUserScenarioValidateOK(t *testing.T) {
	if err := validUserScenario().Validate(); err != nil {
		t.Fatalf("valid scenario rejected: %v", err)
	}
}

func TestUserScenarioValidateAllowsEmptyMembership(t *testing.T) {
	sc := validUserScenario()
	sc.ServiceIDs = nil
	if err := sc.Validate(); err != nil {
		t.Fatalf("empty membership rejected: %v", err)
	}
}

func TestUserScenarioValidateRejectsBlankName(t *testing.T) {
	sc := validUserScenario()
	sc.Name = "   "
	if err := sc.Validate(); err == nil {
		t.Fatal("blank name: want error, got nil")
	}
}

func TestUserScenarioValidateRejectsDuplicateServiceIDs(t *testing.T) {
	sc := validUserScenario()
	sc.ServiceIDs = []string{"svc-1", "svc-1"}
	if err := sc.Validate(); err == nil {
		t.Fatal("duplicate service id: want error, got nil")
	}
}

func TestUserScenarioValidateRejectsBlankServiceID(t *testing.T) {
	sc := validUserScenario()
	sc.ServiceIDs = []string{"svc-1", "  "}
	if err := sc.Validate(); err == nil {
		t.Fatal("blank service id: want error, got nil")
	}
}
