package transit

import (
	"fmt"
	"strings"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/fault"
)

type UserScenario struct {
	ID                 string                `json:"id"`
	Slug               string                `json:"slug"`
	OwnerID            string                `json:"owner_id"`
	Name               string                `json:"name"`
	Description        string                `json:"description,omitempty"`
	ServiceIDs         []string              `json:"service_ids"`
	InterchangePairs   []InterchangePair     `json:"interchange_pairs,omitempty"`
	CreatedAt          time.Time             `json:"created_at"`
	UpdatedAt          time.Time             `json:"updated_at"`
	BoardingWait       *BoardingWaitOverride `json:"boarding_wait,omitempty"`
	BoardingWaitPolicy string                `json:"boarding_wait_policy,omitempty"`
	BoardingWaitSecs   int                   `json:"boarding_wait_secs"`
	BoardingWaitSource string                `json:"boarding_wait_source,omitempty"`
}

func (s *UserScenario) ResolveBoardingWait(global BoardingWaitPolicy) error {
	policy, source, err := ResolveBoardingWait(nil, s.BoardingWait, global)
	if err != nil {
		return err
	}
	if err := policy.resolveInto(nil, &s.BoardingWaitPolicy, &s.BoardingWaitSecs); err != nil {
		return err
	}
	s.BoardingWaitSource = source
	return nil
}

func (s UserScenario) Validate() error {
	var faults fault.ValidationFaults
	if strings.TrimSpace(s.Name) == "" {
		faults = append(faults, fault.Whole("name", fault.RuleRequired, "name is required"))
	}
	seen := make(map[string]bool, len(s.ServiceIDs))
	members := make(map[string]bool, len(s.ServiceIDs))
	for i, id := range s.ServiceIDs {
		if strings.TrimSpace(id) == "" {
			faults = append(faults, fault.At("service_ids", i, fault.RuleRequired,
				fmt.Sprintf("service_ids[%d]: must not be blank", i)))
			continue
		}
		if seen[id] {
			faults = append(faults, fault.At("service_ids", i, fault.RuleDuplicate,
				fmt.Sprintf("service_ids[%d]: duplicate service id %q", i, id)))
			continue
		}
		seen[id] = true
		members[id] = true
	}

	// Cross-checked against ServiceIDs rather than against the stops a
	// service actually has: this struct has no access to that (a service is
	// loaded separately), so it can confirm a pair names two of the
	// scenario's own members but not that the slugs are real. That is
	// CompileServices' job, at compile time, when both are finally in scope
	// together (see validateInterchangePairs).
	for i, p := range s.InterchangePairs {
		if strings.TrimSpace(p.A.ServiceID) == "" || strings.TrimSpace(p.A.Slug) == "" {
			faults = append(faults, fault.At("interchange_pairs.a", i, fault.RuleRequired,
				fmt.Sprintf("interchange_pairs[%d].a: service_id and slug are required", i)))
		}
		if strings.TrimSpace(p.B.ServiceID) == "" || strings.TrimSpace(p.B.Slug) == "" {
			faults = append(faults, fault.At("interchange_pairs.b", i, fault.RuleRequired,
				fmt.Sprintf("interchange_pairs[%d].b: service_id and slug are required", i)))
		}
		if p.A.ServiceID != "" && p.A.ServiceID == p.B.ServiceID {
			faults = append(faults, fault.At("interchange_pairs", i, fault.RuleSameService,
				fmt.Sprintf("interchange_pairs[%d]: both stops are on service %q, want two different services", i, p.A.ServiceID)))
		}
		if p.A.ServiceID != "" && !members[p.A.ServiceID] {
			faults = append(faults, fault.At("interchange_pairs.a", i, fault.RuleNotMember,
				fmt.Sprintf("interchange_pairs[%d].a: service %q is not a member of this scenario", i, p.A.ServiceID)))
		}
		if p.B.ServiceID != "" && !members[p.B.ServiceID] {
			faults = append(faults, fault.At("interchange_pairs.b", i, fault.RuleNotMember,
				fmt.Sprintf("interchange_pairs[%d].b: service %q is not a member of this scenario", i, p.B.ServiceID)))
		}
	}
	return faults.Err()
}
