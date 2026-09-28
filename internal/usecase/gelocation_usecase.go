package usecase

import "math"

func CalculateDistance(lat1 float64, lon1 float64, lat2 float64, lon2 float64) bool {
	R := 6371.0

	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)

	a := math.Pow(math.Sin(dLat/2), 2) + math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Pow(math.Sin(dLon/2), 2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	distance := R * c

	return distance < 1
}

func toRad(deg float64) float64 {
	return (deg * math.Pi) / 180
}
