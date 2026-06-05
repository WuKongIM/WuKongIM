package transportv2

import "github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"

type RemoteError = core.RemoteError

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
