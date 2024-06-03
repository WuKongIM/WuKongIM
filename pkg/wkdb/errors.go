package wkdb

import "errors"

var (
	ErrMessageNotFound      = errors.New("message not found")
	ErrUserNotExist         = errors.New("user not exist")
	ErrDeviceNotExist       = errors.New("device not exist")
	ErrConversationNotExist = errors.New("conversation not exist")
)
