package entity

type Alert struct {
	ID          string  `gorm:"column:id;primaryKey"`
	SenderID    string  `gorm:"column:sender_id"`
	Longitude   float64 `gorm:"column:longitude"`
	Latitude    float64 `gorm:"column:latitude"`
	TriggeredAt int64   `gorm:"column:triggered_at;autoCreateTime:milli"`
}

func (a *Alert) TableName() string {
	return "alerts"
}
