package auth_test

import (
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func ptr(s string) *string { return &s }

func TestCanAccess(t *testing.T) {
	owner := transit.User{ID: "user-1"}
	other := transit.User{ID: "user-2"}
	admin := transit.User{ID: "user-3", IsAdmin: true}

	tests := []struct {
		name    string
		user    transit.User
		ownerID *string
		want    bool
	}{
		{"owner reaches their own resource", owner, ptr("user-1"), true},
		{"non-owner is denied", other, ptr("user-1"), false},
		{"admin reaches anyone's resource", admin, ptr("user-1"), true},
		// Unowned rows are the seeded/curated platform data. They belong to no
		// user, so only an admin may touch them — otherwise any account could
		// mutate the shared ca-hsr baseline.
		{"unowned resource is admin-only", owner, nil, false},
		{"admin reaches unowned resource", admin, nil, true},
		// A user ID must never match by emptiness.
		{"empty user ID does not match empty owner", transit.User{}, ptr(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := auth.CanAccess(tt.user, tt.ownerID); got != tt.want {
				t.Errorf("CanAccess(%+v, %v) = %v, want %v", tt.user, tt.ownerID, got, tt.want)
			}
		})
	}
}
