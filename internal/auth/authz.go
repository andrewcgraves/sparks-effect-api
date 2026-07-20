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
// That stops being true the moment SPA-80/81 let users create owned scenarios
// and services: GET /api/scenarios reads the compiled store, which holds every
// row, so owned rows would become anonymously readable. Whoever adds that
// authoring path must decide how the public reads exclude owned rows — the
// compiled store deliberately keeps them all, since the graph must compile
// from the full set regardless of who owns what.
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
