package model

import "errors"

var (
	ErrInvalidSender   = errors.New("sender id is required")
	ErrInvalidLocation = errors.New("invalid location")

	// redis GEO Error
	ErrUserLocationNotFound = errors.New("user location not found")

	// SOS lifecycle errors. Controllers map these to HTTP status codes.
	ErrAlertNotFound        = errors.New("alert not found")
	ErrInvalidTransition    = errors.New("invalid alert status transition")
	ErrInvalidPIN           = errors.New("invalid safe PIN")
	ErrPINNotConfigured     = errors.New("safe PIN is not configured for this user")
	ErrRateLimited          = errors.New("SOS rate limit exceeded: max 3 triggers per hour")
	ErrNotAlertOwner        = errors.New("only the alert sender can perform this action")
	ErrNotAlertParticipant  = errors.New("only the sender or a responder can view this alert")
	ErrDuplicateResponse    = errors.New("responder has already responded to this alert")
	ErrStorageNotConfigured = errors.New("media storage is not configured")
)
