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

// SPA-120: a declared pair naming two members is accepted.
func TestUserScenarioValidateAllowsInterchangePairBetweenMembers(t *testing.T) {
	sc := validUserScenario()
	sc.InterchangePairs = []transit.InterchangePair{
		{A: transit.StopIdentity{ServiceID: "svc-1", Slug: "svc-1--a"}, B: transit.StopIdentity{ServiceID: "svc-2", Slug: "svc-2--a"}},
	}
	if err := sc.Validate(); err != nil {
		t.Fatalf("valid interchange pair rejected: %v", err)
	}
}

func TestUserScenarioValidateRejectsInterchangePairOnSameService(t *testing.T) {
	sc := validUserScenario()
	sc.InterchangePairs = []transit.InterchangePair{
		{A: transit.StopIdentity{ServiceID: "svc-1", Slug: "svc-1--a"}, B: transit.StopIdentity{ServiceID: "svc-1", Slug: "svc-1--b"}},
	}
	if err := sc.Validate(); err == nil {
		t.Fatal("same-service interchange pair: want error, got nil")
	}
}

func TestUserScenarioValidateRejectsInterchangePairNamingNonMember(t *testing.T) {
	sc := validUserScenario()
	sc.InterchangePairs = []transit.InterchangePair{
		{A: transit.StopIdentity{ServiceID: "svc-1", Slug: "svc-1--a"}, B: transit.StopIdentity{ServiceID: "svc-not-a-member", Slug: "x--a"}},
	}
	if err := sc.Validate(); err == nil {
		t.Fatal("interchange pair naming a non-member service: want error, got nil")
	}
}

func TestUserScenarioValidateRejectsBlankInterchangePairSlug(t *testing.T) {
	sc := validUserScenario()
	sc.InterchangePairs = []transit.InterchangePair{
		{A: transit.StopIdentity{ServiceID: "svc-1", Slug: "  "}, B: transit.StopIdentity{ServiceID: "svc-2", Slug: "svc-2--a"}},
	}
	if err := sc.Validate(); err == nil {
		t.Fatal("blank interchange pair slug: want error, got nil")
	}
}
