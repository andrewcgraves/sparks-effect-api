package auth

import "github.com/andrewcgraves/sparks-effect-api/internal/transit"

// CanAccess reports whether user may read or mutate a resource owned by
// ownerID. It is the single server-side ownership rule; handlers must consult
// it rather than trusting any owner_id supplied by the client.
//
// Admins reach everything. Otherwise a user reaches only rows they own.
// A nil ownerID means the row is unowned — the seeded, curated platform data
// (e.g. the ca-hsr baseline) — which is admin-only, so no account can quietly
// rewrite shared scenarios.
//
// Note this governs *owned* resources. The public GET endpoints serve curated
// data and remain unauthenticated; this predicate gates the owner-scoped and
// mutating paths.
func CanAccess(user transit.User, ownerID *string) bool {
	if user.IsAdmin {
		return true
	}
	if ownerID == nil {
		return false
	}
	// Guard against a zero-valued user matching a blank owner column: without
	// this, an unidentified caller would "own" every row with owner_id = ''.
	if user.ID == "" {
		return false
	}
	return *ownerID == user.ID
}
