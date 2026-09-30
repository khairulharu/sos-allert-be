package entity

import (
	"time"
)

type Alert struct {
	ID          string
	SenderID    string
	Location    Location
	TriggeredAt time.Time
}

type Location struct {
	Lat float64
	Lng float64
}
