package model

import "time"

// Alert is the notification payload for one incident. Extra fields carry
// the lifecycle state so push/SMS templates can include the emergency type.
type Alert struct {
	ID                string
	SenderId          string
	Location          LocationResponse
	TriggeredAt       time.Time
	Status            string
	EmergencyType     string
	AccuracyMeters    *float64
	BatteryPercentage *int
	// Severity is the optional AI triage score (0..1); 0 means no triage.
	Severity float64
	// Summary is the optional AI triage summary; empty means no triage.
	Summary string
}
