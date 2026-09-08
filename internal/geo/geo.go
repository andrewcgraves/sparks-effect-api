package geo

import "math"

const earthRadiusKm = 6371.0

const (
	WalkSpeedKmH    = 5.0
	BikeSpeedKmH    = 15.0
	DriveSpeedKmH   = 80.0
	TransitSpeedKmH = 40.0
)

func ReachKm(speedKmH float64, budgetMins int) float64 {
	if budgetMins <= 0 {
		return 0
	}
	return speedKmH * float64(budgetMins) / 60.0
}

func HaversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	dLat := (lat2 - lat1) * math.Pi / 180
	dLng := (lng2 - lng1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLng/2)*math.Sin(dLng/2)
	return earthRadiusKm * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
