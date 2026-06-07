package message

import "errors"

var (
	// ErrInvalidCommand reports a malformed send command.
	ErrInvalidCommand = errors.New("internalv2/message: invalid command")
	// ErrSyncLoginUIDRequired reports that a legacy message sync request has no login UID.
	ErrSyncLoginUIDRequired = errors.New("login_uid不能为空！")
	// ErrSyncChannelIDRequired reports that a legacy message sync request has no channel ID.
	ErrSyncChannelIDRequired = errors.New("channel_id不能为空！")
	// ErrSyncChannelTypeRequired reports that a legacy message sync request has no channel type.
	ErrSyncChannelTypeRequired = errors.New("channel_type不能为空！")
	// ErrMessageReaderRequired reports that channel message sync is not configured.
	ErrMessageReaderRequired = errors.New("internalv2/message: message reader required")
	// ErrRequestSubscribersRequireSyncOnce reports that request-scoped sends must be sync_once.
	ErrRequestSubscribersRequireSyncOnce = errors.New("internalv2/message: request subscribers require sync_once")
	// ErrRequestSubscribersConflictChannel reports that request-scoped sends cannot specify a channel.
	ErrRequestSubscribersConflictChannel = errors.New("internalv2/message: request subscribers cannot include channel_id")
	// ErrRequestSubscribersRequired reports that request-scoped sends need at least one usable subscriber.
	ErrRequestSubscribersRequired = errors.New("internalv2/message: request subscribers required")
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
	// ErrAppendResultMissing reports a successful batch append response without a matching item result.
	ErrAppendResultMissing = errors.New("internalv2/message: append result missing")
)
