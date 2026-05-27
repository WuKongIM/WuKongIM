package message

import "errors"

var (
	// ErrInvalidCommand reports a malformed send command.
	ErrInvalidCommand = errors.New("internalv2/message: invalid command")
	// ErrAppenderRequired reports that durable append is not configured.
	ErrAppenderRequired = errors.New("internalv2/message: appender required")
	// ErrMessageIDAllocatorRequired reports that message id allocation is not configured.
	ErrMessageIDAllocatorRequired = errors.New("internalv2/message: message id allocator required")
	// ErrNotLeader reports that the append target is no longer the leader.
	ErrNotLeader = errors.New("internalv2/message: not leader")
	// ErrStaleRoute reports that append used stale channel metadata.
	ErrStaleRoute = errors.New("internalv2/message: stale route")
	// ErrRouteNotReady reports that cluster routing is not ready for foreground writes.
	ErrRouteNotReady = errors.New("internalv2/message: route not ready")
	// ErrChannelNotFound reports that the target channel is not available.
	ErrChannelNotFound = errors.New("internalv2/message: channel not found")
	// ErrBackpressured reports bounded runtime pressure.
	ErrBackpressured = errors.New("internalv2/message: backpressured")
	// ErrAppendFailed wraps unexpected append failures.
	ErrAppendFailed = errors.New("internalv2/message: append failed")
)
