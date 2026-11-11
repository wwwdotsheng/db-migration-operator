package adapter

import "errors"

var (
	ErrVersionConflict           = errors.New("migration version conflict")
	ErrNotParseDriverConfig      = errors.New("failed to parse driver config")
	ErrNotFoundSecret            = errors.New("secret not found")
	ErrUnauthorizedSecret        = errors.New("secret not unauthorized")
	ErrNoAdapterForDriver        = errors.New("no adapter registered for driver")
	ErrServerVersionQuery        = errors.New("failed to query server version")
	ErrUnrecognizedServerVersion = errors.New("unrecognized server version")
	ErrIncompatibleServerVersion = errors.New("incompatible server version")
)
