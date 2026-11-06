package entity

type Location struct {
	ID        string  `gorm:"column:id;primaryKey"`
	UserId    string  `gorm:"column:user_id"`
	Longitude float64 `gorm:"column:longitude"`
	Latitude  float64 `gorm:"column:latitude"`
	CreatedAt int64   `gorm:"column:created_at;autoCreateTime:milli"`
	UpdatedAt int64   `gorm:"column:updated_at;autoCreateTime:milli;autoUpdateTime:milli"`
}

type LocationCache struct {
	UserId    string
	Longitude float64
	Latitude  float64
}

// GeoLocation is one result of a radius search: the user id, their
// position, and the distance from the search center in meters.
type GeoLocation struct {
	UserId         string
	Longitude      float64
	Latitude       float64
	DistanceMeters float64
}

func (c *Location) TableName() string {
	return "locations"
}
