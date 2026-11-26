package entity

// Alert statuses. Lifecycle:
// TRIGGERED -> ACKNOWLEDGED -> RESOLVED
// TRIGGERED/ACKNOWLEDGED -> CANCELLED
// TRIGGERED -> EXPIRED (stale alerts, via ExpireOldAlerts)
const (
	AlertStatusTriggered    = "TRIGGERED"
	AlertStatusAcknowledged = "ACKNOWLEDGED"
	AlertStatusResolved     = "RESOLVED"
	AlertStatusCancelled    = "CANCELLED"
	AlertStatusExpired      = "EXPIRED"
)

// Emergency types accepted by POST /api/sos.
const (
	EmergencyTypeDanger   = "DANGER"
	EmergencyTypeMedical  = "MEDICAL"
	EmergencyTypeAccident = "ACCIDENT"
)

// Alert is the PostgreSQL `alerts` table (alerts v2 schema).
type Alert struct {
	ID                 string   `gorm:"column:id;primaryKey"`
	SenderID           string   `gorm:"column:sender_id"`
	Longitude          float64  `gorm:"column:longitude"`
	Latitude           float64  `gorm:"column:latitude"`
	TriggeredAt        int64    `gorm:"column:triggered_at;autoCreateTime:milli"`
	Status             string   `gorm:"column:status"`
	EmergencyType      string   `gorm:"column:emergency_type"`
	AccuracyMeters     *float64 `gorm:"column:accuracy_meters"`
	BatteryPercentage  *int     `gorm:"column:battery_percentage"`
	Severity           *float64 `gorm:"column:severity"`
	Summary            *string  `gorm:"column:summary"`
	CancellationReason *string  `gorm:"column:cancellation_reason"`
	ResolvedAt         *int64   `gorm:"column:resolved_at"`
	CancelledAt        *int64   `gorm:"column:cancelled_at"`
	// DispatchDueAt is the durable grace-period outbox marker: the fan-out
	// is due at this millisecond timestamp. Nil means the alert was
	// dispatched inline (no grace period), so it never enters the scan.
	DispatchDueAt *int64 `gorm:"column:dispatch_due_at"`
	// DispatchedAt is stamped once the fan-out for DispatchDueAt ran. Not
	// nil (or a NULL DispatchDueAt) keeps the alert out of the due scan.
	DispatchedAt *int64 `gorm:"column:dispatched_at"`
}

func (a *Alert) TableName() string {
	return "alerts"
}

// IsActive reports whether the incident still needs attention.
func (a *Alert) IsActive() bool {
	return a.Status == AlertStatusTriggered || a.Status == AlertStatusAcknowledged
}

// CanTransitionTo reports whether a status change follows the lifecycle.
func (a *Alert) CanTransitionTo(next string) bool {
	switch a.Status {
	case AlertStatusTriggered:
		return next == AlertStatusAcknowledged || next == AlertStatusResolved ||
			next == AlertStatusCancelled || next == AlertStatusExpired
	case AlertStatusAcknowledged:
		return next == AlertStatusResolved || next == AlertStatusCancelled
	default:
		return false
	}
}

// AlertBreadcrumb is one recorded position in an incident trajectory
// (PostgreSQL `alert_breadcrumbs` table).
type AlertBreadcrumb struct {
	ID                int64    `gorm:"column:id;primaryKey;autoIncrement"`
	AlertID           string   `gorm:"column:alert_id"`
	Latitude          float64  `gorm:"column:latitude"`
	Longitude         float64  `gorm:"column:longitude"`
	SpeedMps          *float64 `gorm:"column:speed_mps"`
	HeadingDeg        *float64 `gorm:"column:heading_deg"`
	AltitudeMeters    *float64 `gorm:"column:altitude_meters"`
	AccuracyMeters    *float64 `gorm:"column:accuracy_meters"`
	BatteryPercentage *int     `gorm:"column:battery_percentage"`
	RecordedAt        int64    `gorm:"column:recorded_at"`
}

func (b *AlertBreadcrumb) TableName() string {
	return "alert_breadcrumbs"
}

// AlertResponse records one responder answering an incident
// (PostgreSQL `alert_responses` table).
type AlertResponse struct {
	ID                 string  `gorm:"column:id;primaryKey"`
	AlertID            string  `gorm:"column:alert_id"`
	ResponderID        string  `gorm:"column:responder_id"`
	ResponderLatitude  float64 `gorm:"column:responder_latitude"`
	ResponderLongitude float64 `gorm:"column:responder_longitude"`
	DistanceMeters     float64 `gorm:"column:distance_meters"`
	ETASeconds         int     `gorm:"column:eta_seconds"`
	RespondedAt        int64   `gorm:"column:responded_at;autoCreateTime:milli"`
}

func (r *AlertResponse) TableName() string {
	return "alert_responses"
}

// EmergencyContact is a verified Tier-1 contact for SMS/voice fallback
// (PostgreSQL `emergency_contacts` table). It is separate from the generic
// `contacts` table, which predates the SOS design.
type EmergencyContact struct {
	ID           string `gorm:"column:id;primaryKey"`
	UserID       string `gorm:"column:user_id"`
	ContactName  string `gorm:"column:contact_name"`
	PhoneNumber  string `gorm:"column:phone_number"`
	Relationship string `gorm:"column:relationship"`
	IsVerified   bool   `gorm:"column:is_verified"`
	CreatedAt    int64  `gorm:"column:created_at;autoCreateTime:milli"`
}

func (c *EmergencyContact) TableName() string {
	return "emergency_contacts"
}

// AlertMedia records an uploaded incident media file
// (PostgreSQL `alert_media` table). The bytes live in the configured
// storage backend (local disk by default, S3 when configured).
type AlertMedia struct {
	ID                  string `gorm:"column:id;primaryKey"`
	AlertID             string `gorm:"column:alert_id"`
	Filename            string `gorm:"column:filename"`
	ContentType         string `gorm:"column:content_type"`
	SizeBytes           int64  `gorm:"column:size_bytes"`
	StoragePath         string `gorm:"column:storage_path"`
	UploadedBy          string `gorm:"column:uploaded_by"`
	UploadedAt          int64  `gorm:"column:uploaded_at;autoCreateTime:milli"`
	Transcript          string `gorm:"column:transcript"`
	TranscriptionStatus string `gorm:"column:transcription_status"`
	TranscribedAt       *int64 `gorm:"column:transcribed_at"`
}

func (m *AlertMedia) TableName() string {
	return "alert_media"
}

// AlertRecipient records one user notified for an incident
// (PostgreSQL `alert_recipients` table), backing GET /api/sos/inbox.
type AlertRecipient struct {
	AlertID    string `gorm:"column:alert_id;primaryKey"`
	UserID     string `gorm:"column:user_id;primaryKey"`
	NotifiedAt int64  `gorm:"column:notified_at"`
}

func (r *AlertRecipient) TableName() string {
	return "alert_recipients"
}
