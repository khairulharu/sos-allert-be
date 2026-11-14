package model

// TriggerSOSRequest is the POST /api/sos body from README section 4.
// Latitude/longitude are optional: when omitted, the sender's last known
// Redis GEO position is used (the pre-existing behaviour).
type TriggerSOSRequest struct {
	Latitude          *float64 `json:"latitude" validate:"omitempty,gte=-85.05,lte=85.05"`
	Longitude         *float64 `json:"longitude" validate:"omitempty,gte=-180,lte=180"`
	AccuracyMeters    *float64 `json:"accuracy_meters" validate:"omitempty,gte=0"`
	BatteryPercentage *int     `json:"battery_percentage" validate:"omitempty,gte=0,lte=100"`
	EmergencyType     string   `json:"emergency_type" validate:"omitempty,oneof=DANGER MEDICAL ACCIDENT"`
	// Description feeds the optional AI triage ("ai.*" config). It is
	// advisory only: a missing or failing triage never blocks the SOS.
	Description string `json:"description" validate:"omitempty,max=1000"`
	// ClientSeverity and ClientSummary are the sender's own claim, never
	// the system's assessment: they are stored as-is for labelling in the
	// notification ("pelapor menyebut") and never drive triage or the
	// fan-out radius. The server-side triage comes from the description or
	// from a pending POST /api/sos/classify result.
	ClientSeverity *float64 `json:"client_severity" validate:"omitempty,gte=0,lte=1"`
	ClientSummary  string   `json:"client_summary" validate:"omitempty,max=500"`
}

// TriggerSOSResult is the internal result of a trigger. The HTTP shape is
// SOSCreatedResponse, which matches the README response example.
type TriggerSOSResult struct {
	Alert              Alert
	Recipients         []string
	RadiusMeters       float64
	GracePeriodSeconds int
	Triage             *TriageInfo
}

// TriageInfo is the optional AI triage metadata attached to a trigger:
// the classified emergency type, a 0..1 severity score (drives the
// adaptive fan-out start radius) and a short human-readable summary.
type TriageInfo struct {
	EmergencyType string  `json:"emergency_type,omitempty"`
	Severity      float64 `json:"severity"`
	Summary       string  `json:"summary,omitempty"`
}

// SOSCreatedResponse matches README section 4: 201 Created.
type SOSCreatedResponse struct {
	AlertID            string      `json:"alert_id"`
	Status             string      `json:"status"`
	GracePeriodSeconds int         `json:"grace_period_seconds"`
	RecipientsNotified int         `json:"recipients_notified"`
	TriggeredAt        int64       `json:"triggered_at"`
	Triage             *TriageInfo `json:"triage,omitempty"`
	// ClientSeverity/ClientSummary echo the sender's claim so the client can
	// see that it was recorded as a claim, not as the system triage.
	ClientSeverity *float64 `json:"client_severity,omitempty"`
	ClientSummary  string   `json:"client_summary,omitempty"`
}

// CancelSOSRequest is the POST /api/sos/:alertId/cancel body.
type CancelSOSRequest struct {
	PIN    string `json:"pin" validate:"required,len=4,numeric"`
	Reason string `json:"reason" validate:"omitempty,oneof=FALSE_ALARM ACCIDENTAL SAFE_NOW TEST"`
}

// CancelSOSResponse matches the README cancel example.
type CancelSOSResponse struct {
	AlertID     string `json:"alert_id"`
	Status      string `json:"status"`
	CancelledAt int64  `json:"cancelled_at"`
}

// RespondSOSRequest is the POST /api/sos/:alertId/respond body.
type RespondSOSRequest struct {
	ResponderLatitude  *float64 `json:"responder_latitude" validate:"required,gte=-85.05,lte=85.05"`
	ResponderLongitude *float64 `json:"responder_longitude" validate:"required,gte=-180,lte=180"`
}

// RespondSOSResponse matches the README respond example.
type RespondSOSResponse struct {
	ResponseID           string `json:"response_id"`
	ETASeconds           int    `json:"eta_seconds"`
	VictimDistanceMeters int    `json:"victim_distance_meters"`
}

// BreadcrumbRequest is one live-tracking point, via REST or WebSocket.
type BreadcrumbRequest struct {
	Latitude          *float64 `json:"latitude" validate:"required,gte=-85.05,lte=85.05"`
	Longitude         *float64 `json:"longitude" validate:"required,gte=-180,lte=180"`
	SpeedMps          *float64 `json:"speed_mps" validate:"omitempty,gte=0"`
	HeadingDeg        *float64 `json:"heading_deg" validate:"omitempty,gte=0,lte=360"`
	AltitudeMeters    *float64 `json:"altitude_meters" validate:"omitempty"`
	AccuracyMeters    *float64 `json:"accuracy_meters" validate:"omitempty,gte=0"`
	BatteryPercentage *int     `json:"battery_percentage" validate:"omitempty,gte=0,lte=100"`
	Timestamp         *int64   `json:"timestamp" validate:"omitempty,gt=0"`
}

// BreadcrumbResponse is a stored breadcrumb.
type BreadcrumbResponse struct {
	Latitude          float64  `json:"latitude"`
	Longitude         float64  `json:"longitude"`
	SpeedMps          *float64 `json:"speed_mps,omitempty"`
	HeadingDeg        *float64 `json:"heading_deg,omitempty"`
	AltitudeMeters    *float64 `json:"altitude_meters,omitempty"`
	AccuracyMeters    *float64 `json:"accuracy_meters,omitempty"`
	BatteryPercentage *int     `json:"battery_percentage,omitempty"`
	RecordedAt        int64    `json:"recorded_at"`
}

// LocationUpdateMessage is the WebSocket server -> responder broadcast.
type LocationUpdateMessage struct {
	Type       string   `json:"type"`
	AlertID    string   `json:"alert_id"`
	Latitude   float64  `json:"latitude"`
	Longitude  float64  `json:"longitude"`
	SpeedMps   *float64 `json:"speed_mps,omitempty"`
	HeadingDeg *float64 `json:"heading_deg,omitempty"`
}

// SenderSummary is the victim as seen by a responder.
type SenderSummary struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// ResponderSummary is one responder en route, with the distance and ETA
// recorded when they answered.
type ResponderSummary struct {
	UserID         string `json:"user_id"`
	Name           string `json:"name,omitempty"`
	DistanceMeters int    `json:"distance_m"`
	ETASeconds     int    `json:"eta_seconds"`
	RespondedAt    int64  `json:"responded_at"`
}

// AlertDetailResponse matches README GET /api/sos/:alertId.
// LatestLocation is masked (rounded) for viewers who are neither the
// sender nor a responder; Masked reports that.
type AlertDetailResponse struct {
	AlertID           string        `json:"alert_id"`
	Sender            SenderSummary `json:"sender"`
	Status            string        `json:"status"`
	EmergencyType     string        `json:"emergency_type"`
	Severity          float64       `json:"severity,omitempty"`
	Summary           string        `json:"summary,omitempty"`
	InitialLocation   LocationPoint `json:"initial_location"`
	LatestLocation    LocationPoint `json:"latest_location"`
	Masked            bool          `json:"masked,omitempty"`
	RespondersEnRoute int           `json:"responders_en_route"`
	// Responders lists who is coming with distance/ETA. Only populated
	// for the incident's participants (sender or responder); other
	// viewers keep the bare count.
	Responders  []ResponderSummary   `json:"responders,omitempty"`
	TriggeredAt int64                `json:"triggered_at"`
	ResolvedAt  *int64               `json:"resolved_at,omitempty"`
	CancelledAt *int64               `json:"cancelled_at,omitempty"`
	Breadcrumbs []BreadcrumbResponse `json:"breadcrumbs,omitempty"`
}

// LocationPoint is a lat/lng pair with an optional timestamp.
type LocationPoint struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	UpdatedAt *int64  `json:"updated_at,omitempty"`
}

// SearchAlertHistoryRequest is the GET /api/sos/history query. The
// controller fills the fields from the query string; validation happens
// in the use case so both sides share one rule set.
type SearchAlertHistoryRequest struct {
	UserID string `json:"-" validate:"required"`
	Role   string `json:"-" validate:"required,oneof=sent received"`
	Status string `json:"-" validate:"omitempty,oneof=TRIGGERED ACKNOWLEDGED RESOLVED CANCELLED EXPIRED active closed"`
	Page   int    `json:"-" validate:"min=1"`
	Size   int    `json:"-" validate:"min=1,max=100"`
	FromMs *int64 `json:"-" validate:"omitempty,gt=0"`
	ToMs   *int64 `json:"-" validate:"omitempty,gt=0"`
}

// AlertStatusCounts is the per-status breakdown behind the summary
// cards. Active groups TRIGGERED + ACKNOWLEDGED; Total is the sum of
// every bucket.
type AlertStatusCounts struct {
	Total     int64 `json:"total"`
	Active    int64 `json:"active"`
	Resolved  int64 `json:"resolved"`
	Cancelled int64 `json:"cancelled"`
	Expired   int64 `json:"expired"`
}

// AlertRespondedCounts is how many incidents the caller answered.
type AlertRespondedCounts struct {
	Total    int64 `json:"total"`
	Resolved int64 `json:"resolved"`
}

// AlertSummaryResponse is GET /api/sos/summary: aggregate cards over
// every status, so the UI never derives them from the visible page.
type AlertSummaryResponse struct {
	Sent      AlertStatusCounts    `json:"sent"`
	Received  AlertStatusCounts    `json:"received"`
	Responded AlertRespondedCounts `json:"responded"`
}

// CreateEmergencyContactRequest is the POST /api/emergency-contacts body.
type CreateEmergencyContactRequest struct {
	UserID       string `json:"-" validate:"required"`
	ContactName  string `json:"contact_name" validate:"required,max=100"`
	PhoneNumber  string `json:"phone_number" validate:"required,max=30"`
	Relationship string `json:"relationship" validate:"required,max=50"`
}

// EmergencyContactResponse is a stored Tier-1 contact.
type EmergencyContactResponse struct {
	ID           string `json:"id"`
	ContactName  string `json:"contact_name"`
	PhoneNumber  string `json:"phone_number"`
	Relationship string `json:"relationship"`
	IsVerified   bool   `json:"is_verified"`
	CreatedAt    int64  `json:"created_at"`
}

// AlertMediaResponse is a stored incident media record.
type AlertMediaResponse struct {
	ID          string `json:"id"`
	AlertID     string `json:"alert_id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	UploadedAt  int64  `json:"uploaded_at"`
	// URL is the authenticated download path (GET, Authorization header).
	URL string `json:"url"`
	// Transcript is the speech-to-text result for audio evidence.
	Transcript string `json:"transcript,omitempty"`
	// TranscriptionStatus: disabled | too_large | completed | empty | failed.
	TranscriptionStatus string      `json:"transcription_status"`
	TranscribedAt       *int64      `json:"transcribed_at,omitempty"`
	Triage              *TriageInfo `json:"triage,omitempty"`
	// Escalated reports that the audio transcription raised the incident
	// severity enough to widen the responder fan-out; RecipientsAdded is
	// how many newly reached responders were notified for it.
	Escalated       bool `json:"escalated,omitempty"`
	RecipientsAdded int  `json:"recipients_added,omitempty"`
}

// ClassifyResponse is the POST /api/sos/classify result: the
// audio-derived classification plus its transcript. A client can pass
// emergency_type/severity/summary into POST /api/sos so the first
// fan-out already uses them.
type ClassifyResponse struct {
	EmergencyType       string  `json:"emergency_type"`
	Severity            float64 `json:"severity"`
	Summary             string  `json:"summary,omitempty"`
	Transcript          string  `json:"transcript,omitempty"`
	TranscriptionStatus string  `json:"transcription_status"`
}

// AlertTriageMessage is broadcast when audio transcription updates the
// incident classification after the initial trigger.
type AlertTriageMessage struct {
	Type          string  `json:"type"`
	AlertID       string  `json:"alert_id"`
	EmergencyType string  `json:"emergency_type"`
	Severity      float64 `json:"severity"`
	Summary       string  `json:"summary,omitempty"`
	UpdatedAt     int64   `json:"updated_at"`
}

// ResponderAddedMessage is broadcast on the tracking WebSocket when a
// responder answers, so an open view can append them live.
type ResponderAddedMessage struct {
	Type           string `json:"type"`
	AlertID        string `json:"alert_id"`
	ResponderID    string `json:"responder_id"`
	Name           string `json:"name,omitempty"`
	DistanceMeters int    `json:"distance_m"`
	ETASeconds     int    `json:"eta_seconds"`
	RespondedAt    int64  `json:"responded_at"`
}

// AlertStatusMessage is broadcast on the tracking WebSocket when an
// incident changes status (cancel/acknowledge/resolve).
type AlertStatusMessage struct {
	Type      string `json:"type"`
	AlertID   string `json:"alert_id"`
	Status    string `json:"status"`
	UpdatedAt int64  `json:"updated_at"`
}

// MaskCoordinates rounds a position to ~100 m (3 decimal degrees) so
// unconfirmed crowd responders never see the victim's exact address.
// The sender and confirmed responders always get exact coordinates.
func MaskCoordinates(latitude, longitude float64) (float64, float64) {
	const factor = 1000.0
	return round3(latitude, factor), round3(longitude, factor)
}

func round3(v, factor float64) float64 {
	if v >= 0 {
		return float64(int(v*factor+0.5)) / factor
	}
	return -float64(int(-v*factor+0.5)) / factor
}

// SOSEvent is the Kafka payload for the sos.dispatched topic.
type SOSEvent struct {
	EventType     string   `json:"event_type"`
	AlertID       string   `json:"alert_id"`
	SenderID      string   `json:"sender_id"`
	Latitude      float64  `json:"latitude"`
	Longitude     float64  `json:"longitude"`
	EmergencyType string   `json:"emergency_type"`
	Status        string   `json:"status"`
	Recipients    []string `json:"recipients"`
	TriggeredAt   int64    `json:"triggered_at"`
	// Severity/Summary carry the server-side triage into the worker so
	// push content is identical whether dispatch is direct or via Kafka.
	Severity float64 `json:"severity,omitempty"`
	Summary  string  `json:"summary,omitempty"`
	// ClientSeverity/ClientSummary are the sender's claim, labelled as such
	// in the notification and never used for the fan-out decision.
	ClientSeverity *float64 `json:"client_severity,omitempty"`
	ClientSummary  string   `json:"client_summary,omitempty"`
}
