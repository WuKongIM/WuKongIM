package multiraft

import "errors"

var (
	ErrInvalidOptions      = errors.New("multiraft: invalid options")
	ErrSlotExists          = errors.New("multiraft: slot already exists")
	ErrSlotNotFound        = errors.New("multiraft: slot not found")
	ErrSlotClosed          = errors.New("multiraft: slot closed")
	ErrRuntimeClosed       = errors.New("multiraft: runtime closed")
	ErrNotLeader           = errors.New("multiraft: not leader")
	ErrConfigChangePending = errors.New("multiraft: config change pending")
	errNotImplemented      = errors.New("multiraft: not implemented")
)
