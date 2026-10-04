package model

import "time"

type Alert struct {
	ID          string
	SenderId    string
	Location    LocationResponse
	TriggeredAt time.Time
}
