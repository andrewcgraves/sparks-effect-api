package transit

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// UserScenario is a user-owned curated set of UserService IDs.
//
// Deliberately separate from the seeded Scenario/scenario_service pair: that
// pipeline compiles every scenario row into a public TransitGraph via
// LoadStore, and membership there is auto-included by matching a service's
// scenario_id FK. A UserScenario has no FK-matching, no route/station catalog,
// and never enters the compiled read path — membership is exactly the set of
// UserService IDs the owner chose, nothing more. Keeping it apart mirrors why
// UserService is kept apart from Service.
type UserScenario struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	OwnerID     string   `json:"owner_id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	ServiceIDs  []string `json:"service_ids"`
	// InterchangePairs is SPA-120's declared interchange: this scenario's
	// assertion that two stops, each on a member service, are the same
	// place. See InterchangePair.
	InterchangePairs []InterchangePair `json:"interchange_pairs,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	// BoardingWait is the stored override; nil means inherit from the global
	// default. A member service with its own override still wins.
	BoardingWait *BoardingWaitOverride `json:"boarding_wait,omitempty"`
	// BoardingWaitPolicy, BoardingWaitSecs, and BoardingWaitSource are the
	// resolved override a compile of this scenario would apply to members
	// that have none of their own. Response-only — filled by the read
	// handlers. Secs is 0 for headway policies: a scenario has no frequency
	// windows of its own, so half_headway / full_headway cannot produce a
	// number until they meet a service.
	BoardingWaitPolicy string `json:"boarding_wait_policy,omitempty"`
	BoardingWaitSecs   int    `json:"boarding_wait_secs"`
	BoardingWaitSource string `json:"boarding_wait_source,omitempty"`
}

// ResolveBoardingWait fills the response-only boarding-wait fields from this
// scenario's override and the global default.
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

// Validate reports whether the scenario is well-formed enough to persist. It
// checks caller-supplied fields only — ID, Slug, and OwnerID are assigned by
// the server, not the client. An empty membership set is allowed: a caller may
// create the shell first and populate it via update.
func (s UserScenario) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("name is required")
	}
	seen := make(map[string]bool, len(s.ServiceIDs))
	members := make(map[string]bool, len(s.ServiceIDs))
	for i, id := range s.ServiceIDs {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("service_ids[%d]: must not be blank", i)
		}
		if seen[id] {
			return fmt.Errorf("service_ids[%d]: duplicate service id %q", i, id)
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
			return fmt.Errorf("interchange_pairs[%d].a: service_id and slug are required", i)
		}
		if strings.TrimSpace(p.B.ServiceID) == "" || strings.TrimSpace(p.B.Slug) == "" {
			return fmt.Errorf("interchange_pairs[%d].b: service_id and slug are required", i)
		}
		if p.A.ServiceID == p.B.ServiceID {
			return fmt.Errorf("interchange_pairs[%d]: both stops are on service %q, want two different services", i, p.A.ServiceID)
		}
		if !members[p.A.ServiceID] {
			return fmt.Errorf("interchange_pairs[%d].a: service %q is not a member of this scenario", i, p.A.ServiceID)
		}
		if !members[p.B.ServiceID] {
			return fmt.Errorf("interchange_pairs[%d].b: service %q is not a member of this scenario", i, p.B.ServiceID)
		}
	}
	return nil
}
