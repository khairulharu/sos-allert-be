package model

// WSAuthRequest is the first frame a browser client sends on the SOS
// tracking WebSocket, replacing the session token that used to travel in the
// URL query string (and proxy access logs).
type WSAuthRequest struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}
