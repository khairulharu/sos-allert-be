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
	// Severity is the server-side triage score (0..1) carried in the
	// notification payload. Unlike the API detail response, the
	// notification path keeps 0 as "nothing to show", so a stored 0 and
	// a never-classified alert look the same here (#55).
	Severity float64
	// Summary is the server-side triage summary; empty means no triage.
	Summary string
	// ClientSeverity and ClientSummary are the sender's claim. They are
	// labelled as a claim in the notification and never affect triage.
	ClientSeverity *float64
	ClientSummary  string
}
