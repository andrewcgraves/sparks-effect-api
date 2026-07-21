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
// Note this governs *owned* resources: it gates the owner-scoped reads and the
// mutating paths. It is not consulted by the public GET endpoints, which today
// serve only unowned curated data because that is all the seed produces.
//
// SPA-80 (UserService) and SPA-81 (UserScenario) both resolved the risk this
// comment used to warn about — a user-authored row leaking into the public
// compiled store — the same way: by keeping user-authored content in its own
// type and table, entirely outside the seeded Scenario/Service pair that
// GET /api/scenarios compiles and serves. Nothing user-owned ever reaches
// LoadStore, so the compiled store and its public reads are unaffected by
// either feature.
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
