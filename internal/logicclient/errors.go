package logicclient

import "errors"

var (
	ErrAuthFailed error = errors.New("auth failed")
	ErrRespStatus error = errors.New("resp status is not ok")
)
