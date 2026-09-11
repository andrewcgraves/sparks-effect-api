package auth

import "github.com/andrewcgraves/sparks-effect-api/internal/account"

func CanAccess(user account.User, ownerID *string) bool {
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

func CanReference(user account.User, ownerID *string) bool {
	return ownerID == nil || CanAccess(user, ownerID)
}
