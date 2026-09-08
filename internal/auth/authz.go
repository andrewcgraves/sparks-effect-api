package auth

import "github.com/andrewcgraves/sparks-effect-api/internal/transit"

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

func CanReference(user transit.User, ownerID *string) bool {
	return ownerID == nil || CanAccess(user, ownerID)
}
