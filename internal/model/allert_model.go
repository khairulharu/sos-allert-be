package model

import "time"

// Alert is the notification payload for one incident. Extra fields carry
// the lifecycle state so push/SMS templates can include the emergency type.
// AI fields carry the triage summary used by push templates; the
// reporter's original text is never put in the push data map.
type Alert struct {
	ID                string
	SenderId          string
	Location          LocationResponse
	TriggeredAt       time.Time
	Status            string
	EmergencyType     string
	AccuracyMeters    *float64
	BatteryPercentage *int
	AICategory        string
	AISeverity        int
	AIConfidence      float64
	AISummaryID       string
	AISummaryEN       string
	AISource          string
}
