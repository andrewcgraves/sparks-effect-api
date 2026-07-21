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
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	OwnerID     string    `json:"owner_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	ServiceIDs  []string  `json:"service_ids"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
	for i, id := range s.ServiceIDs {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("service_ids[%d]: must not be blank", i)
		}
		if seen[id] {
			return fmt.Errorf("service_ids[%d]: duplicate service id %q", i, id)
		}
		seen[id] = true
	}
	return nil
}
