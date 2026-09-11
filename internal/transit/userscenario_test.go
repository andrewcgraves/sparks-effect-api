package transit_test

import (
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/fault"
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
	got := mustValidationFaults(t, sc.Validate())
	if len(got) != 1 {
		t.Fatalf("got %d faults, want 1: %+v", len(got), got)
	}
	assertFault(t, got[0], "name", fault.RuleRequired, nil)
}

func TestUserScenarioValidateRejectsDuplicateServiceIDs(t *testing.T) {
	sc := validUserScenario()
	sc.ServiceIDs = []string{"svc-1", "svc-1"}
	got := mustValidationFaults(t, sc.Validate())
	if len(got) != 1 {
		t.Fatalf("got %d faults, want 1: %+v", len(got), got)
	}
	assertFault(t, got[0], "service_ids", fault.RuleDuplicate, fault.Index(1))
}

func TestUserScenarioValidateRejectsBlankServiceID(t *testing.T) {
	sc := validUserScenario()
	sc.ServiceIDs = []string{"svc-1", "  "}
	got := mustValidationFaults(t, sc.Validate())
	if len(got) != 1 {
		t.Fatalf("got %d faults, want 1: %+v", len(got), got)
	}
	assertFault(t, got[0], "service_ids", fault.RuleRequired, fault.Index(1))
}

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
	got := mustValidationFaults(t, sc.Validate())
	if len(got) != 1 {
		t.Fatalf("got %d faults, want 1: %+v", len(got), got)
	}
	assertFault(t, got[0], "interchange_pairs", fault.RuleSameService, fault.Index(0))
}

func TestUserScenarioValidateRejectsInterchangePairNamingNonMember(t *testing.T) {
	sc := validUserScenario()
	sc.InterchangePairs = []transit.InterchangePair{
		{A: transit.StopIdentity{ServiceID: "svc-1", Slug: "svc-1--a"}, B: transit.StopIdentity{ServiceID: "svc-not-a-member", Slug: "x--a"}},
	}
	got := mustValidationFaults(t, sc.Validate())
	if len(got) != 1 {
		t.Fatalf("got %d faults, want 1: %+v", len(got), got)
	}
	assertFault(t, got[0], "interchange_pairs.b", fault.RuleNotMember, fault.Index(0))
}

func TestUserScenarioValidateRejectsBlankInterchangePairSlug(t *testing.T) {
	sc := validUserScenario()
	sc.InterchangePairs = []transit.InterchangePair{
		{A: transit.StopIdentity{ServiceID: "svc-1", Slug: "  "}, B: transit.StopIdentity{ServiceID: "svc-2", Slug: "svc-2--a"}},
	}
	got := mustValidationFaults(t, sc.Validate())
	if len(got) != 1 {
		t.Fatalf("got %d faults, want 1: %+v", len(got), got)
	}
	assertFault(t, got[0], "interchange_pairs.a", fault.RuleRequired, fault.Index(0))
}

func TestUserScenarioValidateReportsEveryBadServiceID(t *testing.T) {
	sc := validUserScenario()
	sc.ServiceIDs = []string{"svc-1", "  ", "svc-1"}
	got := mustValidationFaults(t, sc.Validate())
	if len(got) != 2 {
		t.Fatalf("got %d faults, want 2: %+v", len(got), got)
	}
	assertFault(t, got[0], "service_ids", fault.RuleRequired, fault.Index(1))
	assertFault(t, got[1], "service_ids", fault.RuleDuplicate, fault.Index(2))
}
