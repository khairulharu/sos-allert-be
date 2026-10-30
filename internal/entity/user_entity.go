package entity

// User is a struct that represents a user entity
type User struct {
	ID          string    `gorm:"column:id;primaryKey"`
	Password    string    `gorm:"column:password"`
	Name        string    `gorm:"column:name"`
	Token       string    `gorm:"column:token"`
	SafePINHash *string   `gorm:"column:safe_pin_hash"`
	FCMToken    *string   `gorm:"column:fcm_token"`
	APNsToken   *string   `gorm:"column:apns_token"`
	CreatedAt   int64     `gorm:"column:created_at;autoCreateTime:milli"`
	UpdatedAt   int64     `gorm:"column:updated_at;autoCreateTime:milli;autoUpdateTime:milli"`
	Contacts    []Contact `gorm:"foreignKey:user_id;references:id"`
}

func (u *User) TableName() string {
	return "users"
}
