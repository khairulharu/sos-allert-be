package entity

import (
	"time"
)

type Alert struct {
	ID           string
	SenderID     string
	UserLocation UserLocation
	TriggeredAt  time.Time
}
