package entity

type Location struct {
	ID        string  `gorm:"column:id;primaryKey"`
	UserId    string  `gorm:"column:user_id"`
	Longitude float64 `gorm:"column:longitude"`
	Latitude  float64 `gorm:"column:latitude"`
	CreatedAt int64   `gorm:"column:created_at;autoCreateTime:milli"`
	UpdatedAt int64   `gorm:"column:updated_at;autoCreateTime:milli;autoUpdateTime:milli"`
}

func (c *Location) TableName() string {
	return "locations"
}
