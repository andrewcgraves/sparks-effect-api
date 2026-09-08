package handler

import (
	"context"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func mayReachScenario(ctx context.Context, sc transit.Scenario) bool {
	if sc.OwnerID == nil {
		return true
	}
	user, ok := auth.UserFrom(ctx)
	return ok && auth.CanAccess(user, sc.OwnerID)
}
