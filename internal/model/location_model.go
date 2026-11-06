package model

type LocationResponse struct {
	ID        string  `json:"id"`
	UserId    string  `json:"user_id"`
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
}

type UpdateLocationRequest struct {
	UserId    string   `json:"-" validate:"required"`
	Longitude *float64 `json:"longitude" validate:"required,gte=-180,lte=180"`
	Latitude  *float64 `json:"latitude"  validate:"required,gte=-85.05,lte=85.05"`
}

// LocationStatusResponse is the authenticated user's last heartbeat.
// Stale mirrors the >2 minute filter used for SOS fan-out: a stale
// position still exists but the user is no longer an active responder.
type LocationStatusResponse struct {
	UserId    string  `json:"user_id"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	LastSeen  int64   `json:"last_seen"`
	Stale     bool    `json:"stale"`
}

// NearbyResponder is one active user inside the requested radius.
// Coordinates are always rounded (~100 m) before leaving the server.
type NearbyResponder struct {
	UserId         string  `json:"user_id"`
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	DistanceMeters int     `json:"distance_m"`
	LastSeen       int64   `json:"last_seen"`
}

// NearbyLocationResponse is the map payload for nearby responders.
type NearbyLocationResponse struct {
	RadiusMeters float64           `json:"radius_m"`
	Count        int               `json:"count"`
	Responders   []NearbyResponder `json:"responders"`
	Masked       bool              `json:"masked"`
}
