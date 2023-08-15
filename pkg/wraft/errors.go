package wraft

import "errors"

var (
	ErrStopped = errors.New("raft: server stopped")
)
