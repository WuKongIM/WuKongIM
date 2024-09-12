package wkdb

import "errors"

var (
	// ErrMessageNotFound      = errors.New("message not found")
	// ErrUserNotExist         = errors.New("user not exist")
	// ErrDeviceNotExist       = errors.New("device not exist")
	// ErrConversationNotExist = errors.New("conversation not exist")
	// ErrSessionNotExist      = errors.New("session not exist")
	ErrNotFound        = errors.New("not found")
	ErrInvalidUserId   = errors.New("invalid user id")
	ErrInvalidDeviceId = errors.New("invalid device id")
	ErrAlreadyExist    = errors.New("already exist")
)
