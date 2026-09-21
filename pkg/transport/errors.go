package transport

import "github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"

type RemoteError = core.RemoteError

const (
	// RemoteErrorCodeGeneric identifies an unclassified remote handler error.
	RemoteErrorCodeGeneric = core.RemoteErrorCodeGeneric
	// RemoteErrorCodeServiceNotFound identifies an RPC service absent on the remote node.
	RemoteErrorCodeServiceNotFound = core.RemoteErrorCodeServiceNotFound
	// Typed remote failures support errors.Is while retaining remote origin.
	RemoteErrorCodeTimeout  = core.RemoteErrorCodeTimeout
	RemoteErrorCodeCanceled = core.RemoteErrorCodeCanceled
	RemoteErrorCodeBusy     = core.RemoteErrorCodeBusy
	RemoteErrorCodeStopped  = core.RemoteErrorCodeStopped
)

var (
	ErrStopped         = core.ErrStopped
	ErrTimeout         = core.ErrTimeout
	ErrCanceled        = core.ErrCanceled
	ErrNodeNotFound    = core.ErrNodeNotFound
	ErrQueueFull       = core.ErrQueueFull
	ErrMsgTooLarge     = core.ErrMsgTooLarge
	ErrInvalidFrame    = core.ErrInvalidFrame
	ErrInvalidPriority = core.ErrInvalidPriority
	ErrDialFailed      = core.ErrDialFailed
	ErrBusy            = core.ErrBusy
	ErrInvalidConfig   = core.ErrInvalidConfig
)
