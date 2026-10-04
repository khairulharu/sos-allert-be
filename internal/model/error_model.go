package model

import "errors"

var (
	ErrInvalidSender   = errors.New("sender id is required")
	ErrInvalidLocation = errors.New("invalid location")

	// redis GEO Error
	ErrUserLocationNotFound = errors.New("user location not found")
)
