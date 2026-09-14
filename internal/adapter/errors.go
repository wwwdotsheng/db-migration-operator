package adapter

import "errors"

var (
	ErrVersionConflict = errors.New("migration version conflict")
)
