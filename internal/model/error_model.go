package model

import "errors"

var (
	ErrInvalidSender   = errors.New("sender id is required")
	ErrInvalidLocation = errors.New("invalid location")
)
