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
}

// TriggerSOSResult is the internal result of a trigger. The HTTP shape is
// SOSCreatedResponse, which matches the README response example.
type TriggerSOSResult struct {
	Alert              Alert
	Recipients         []string
	RadiusMeters       float64
	GracePeriodSeconds int
}

// SOSCreatedResponse matches README section 4: 201 Created.
type SOSCreatedResponse struct {
	AlertID            string `json:"alert_id"`
	Status             string `json:"status"`
	GracePeriodSeconds int    `json:"grace_period_seconds"`
	RecipientsNotified int    `json:"recipients_notified"`
	TriggeredAt        int64  `json:"triggered_at"`
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

// AlertDetailResponse matches README GET /api/sos/:alertId.
// LatestLocation is masked (rounded) for viewers who are neither the
// sender nor a responder; Masked reports that.
type AlertDetailResponse struct {
	AlertID           string               `json:"alert_id"`
	Sender            SenderSummary        `json:"sender"`
	Status            string               `json:"status"`
	EmergencyType     string               `json:"emergency_type"`
	InitialLocation   LocationPoint        `json:"initial_location"`
	LatestLocation    LocationPoint        `json:"latest_location"`
	Masked            bool                 `json:"masked,omitempty"`
	RespondersEnRoute int                  `json:"responders_en_route"`
	TriggeredAt       int64                `json:"triggered_at"`
	ResolvedAt        *int64               `json:"resolved_at,omitempty"`
	CancelledAt       *int64               `json:"cancelled_at,omitempty"`
	Breadcrumbs       []BreadcrumbResponse `json:"breadcrumbs,omitempty"`
}

// LocationPoint is a lat/lng pair with an optional timestamp.
type LocationPoint struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	UpdatedAt *int64  `json:"updated_at,omitempty"`
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
}
