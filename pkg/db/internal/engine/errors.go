package engine

import "errors"

var (
	errInvalidArgument = errors.New("db: invalid argument")
	errClosed          = errors.New("db: closed")
)
