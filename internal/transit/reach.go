package transit

import (
	"math"

	"github.com/andrewcgraves/sparks-effect-api/internal/geo"
)

func modeSpeedKmH(m TravelMode) (float64, bool) {
	switch m {
	case TravelModeWalk:
		return geo.WalkSpeedKmH, true
	case TravelModeBike:
		return geo.BikeSpeedKmH, true
	case TravelModeDrive:
		return geo.DriveSpeedKmH, true
	case TravelModeTransit:
		return geo.TransitSpeedKmH, true
	default:
		return 0, false
	}
}

type OriginReach struct {
	NearestSlug string
	NearestKm   float64
	MaxReachKm  float64
	InRange     bool
}

func CheckOriginReach(g *TransitGraph, lat, lng float64, mode TravelMode, budgetMins int) (OriginReach, bool) {
	if g == nil || len(g.Nodes) == 0 {
		return OriginReach{}, false
	}
	speed, ok := modeSpeedKmH(mode)
	if !ok {
		return OriginReach{}, false
	}

	reach := geo.ReachKm(speed, budgetMins)

	nearestKm := math.Inf(1)
	nearestSlug := ""
	for _, n := range g.Nodes {
		if d := geo.HaversineKm(lat, lng, n.Lat, n.Lng); d < nearestKm {
			nearestKm = d
			nearestSlug = n.Slug
		}
	}

	return OriginReach{
		NearestSlug: nearestSlug,
		NearestKm:   nearestKm,
		MaxReachKm:  reach,
		InRange:     nearestKm <= reach,
	}, true
}
