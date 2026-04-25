package app

import "errors"

var (
	ErrInvalidConfig  = errors.New("app: invalid config")
	ErrNotBuilt       = errors.New("app: runtime not built")
	ErrAlreadyStarted = errors.New("app: already started")
	ErrStopped        = errors.New("app: stopped")
)
