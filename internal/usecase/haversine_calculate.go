package usecase

import "math"

// CalculateDistanceMeters returns the Haversine great-circle distance
// between two points in meters.
func CalculateDistanceMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusMeters = 6371000.0
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	a := math.Pow(math.Sin(dLat/2), 2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Pow(math.Sin(dLon/2), 2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusMeters * c
}

func CalculateDistance(lat1 float64, lon1 float64, lat2 float64, lon2 float64) bool {
	return CalculateDistanceMeters(lat1, lon1, lat2, lon2) < 1000
}

func toRad(deg float64) float64 {
	return (deg * math.Pi) / 180
}

// walkingSpeedMps is the assumed responder travel speed used for ETA:
// 1.4 m/s (~5 km/h walking). README's example (240 m -> 180 s) implies
// ~1.33 m/s, so this is the same order and is documented as approximate.
const walkingSpeedMps = 1.4

// EstimateETASeconds converts a distance to a walking ETA, at least 60 s.
func EstimateETASeconds(distanceMeters float64) int {
	if distanceMeters <= 0 {
		return 60
	}
	eta := int(distanceMeters/walkingSpeedMps + 0.5)
	if eta < 60 {
		return 60
	}
	return eta
}
