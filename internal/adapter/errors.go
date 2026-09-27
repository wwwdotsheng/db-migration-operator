package adapter

import "errors"

var (
	ErrVersionConflict      = errors.New("migration version conflict")
	ErrNotParseDriverConfig = errors.New("failed to parse driver config")
	ErrNotFoundSecret       = errors.New("secret not found")
	ErrUnauthorizedSecret   = errors.New("secret not unauthorized")
	ErrNoAdapterForDriver   = errors.New("no adapter registered for driver")
)
