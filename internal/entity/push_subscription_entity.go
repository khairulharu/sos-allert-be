package entity

// PushSubscription is one browser Web Push subscription (PostgreSQL
// `push_subscriptions` table). Endpoint is unique and owned by the user
// that registered it: re-subscribing from the same browser refreshes that
// row's keys, while another user cannot take it over.
type PushSubscription struct {
	ID        string `gorm:"column:id;primaryKey"`
	UserID    string `gorm:"column:user_id"`
	Endpoint  string `gorm:"column:endpoint;uniqueIndex"`
	P256DH    string `gorm:"column:p256dh"`
	Auth      string `gorm:"column:auth"`
	UserAgent string `gorm:"column:user_agent"`
	CreatedAt int64  `gorm:"column:created_at;autoCreateTime:milli"`
	UpdatedAt int64  `gorm:"column:updated_at;autoCreateTime:milli;autoUpdateTime:milli"`
}

func (s *PushSubscription) TableName() string {
	return "push_subscriptions"
}
