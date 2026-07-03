package channelappend

import "errors"

var (
	// ErrInvalidCommand reports a malformed send command.
	ErrInvalidCommand = errors.New("internal/message: invalid command")
	// ErrRequestSubscribersRequireSyncOnce reports that request-scoped sends must be sync_once.
	ErrRequestSubscribersRequireSyncOnce = errors.New("internal/message: request subscribers require sync_once")
	// ErrRequestSubscribersConflictChannel reports that request-scoped sends cannot specify a channel.
	ErrRequestSubscribersConflictChannel = errors.New("internal/message: request subscribers cannot include channel_id")
	// ErrRequestSubscribersRequired reports that request-scoped sends need at least one usable subscriber.
	ErrRequestSubscribersRequired = errors.New("internal/message: request subscribers required")
	// ErrAppenderRequired reports that durable append is not configured.
	ErrAppenderRequired = errors.New("internal/message: appender required")
	// ErrMessageIDAllocatorRequired reports that message id allocation is not configured.
	ErrMessageIDAllocatorRequired = errors.New("internal/message: message id allocator required")
	// ErrNotLeader reports that the append target is no longer the leader.
	ErrNotLeader = errors.New("internal/message: not leader")
	// ErrNotChannelAuthority reports that the local node is not the channel authority.
	ErrNotChannelAuthority = errors.New("internal/channelappend: not channel authority")
	// ErrStaleRoute reports that append used stale channel metadata.
	ErrStaleRoute = errors.New("internal/message: stale route")
	// ErrRouteNotReady reports that cluster routing is not ready for foreground writes.
	ErrRouteNotReady = errors.New("internal/message: route not ready")
	// ErrChannelNotFound reports that the target channel is not available.
	ErrChannelNotFound = errors.New("internal/message: channel not found")
	// ErrBackpressured reports bounded runtime pressure.
	ErrBackpressured = errors.New("internal/message: backpressured")
	// ErrChannelBusy reports that channel-level write flow control is saturated.
	ErrChannelBusy = errors.New("internal/channelappend: channel busy")
	// ErrAppendFailed wraps unexpected append failures.
	ErrAppendFailed = errors.New("internal/message: append failed")
	// ErrAppendResultMissing reports a successful batch append response without a matching item result.
	ErrAppendResultMissing = errors.New("internal/message: append result missing")
)
