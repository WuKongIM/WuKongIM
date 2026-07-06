package message

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
)

var (
	// ErrInvalidCommand reports a malformed send command.
	ErrInvalidCommand = channelappend.ErrInvalidCommand
	// ErrSyncLoginUIDRequired reports that a legacy message sync request has no login UID.
	ErrSyncLoginUIDRequired = errors.New("login_uid不能为空！")
	// ErrSyncChannelIDRequired reports that a legacy message sync request has no channel ID.
	ErrSyncChannelIDRequired = errors.New("channel_id不能为空！")
	// ErrSyncChannelTypeRequired reports that a legacy message sync request has no channel type.
	ErrSyncChannelTypeRequired = errors.New("channel_type不能为空！")
	// ErrMessageReaderRequired reports that channel message sync is not configured.
	ErrMessageReaderRequired = errors.New("internal/message: message reader required")
	// ErrMessageEventStoreRequired reports that message event projection storage is not configured.
	ErrMessageEventStoreRequired = errors.New("internal/message: message event store required")
	// ErrMessageEventChannelIDRequired reports that a message event append has no channel ID.
	ErrMessageEventChannelIDRequired = errors.New("channel_id不能为空！")
	// ErrMessageEventChannelTypeRequired reports that a message event append has no channel type.
	ErrMessageEventChannelTypeRequired = errors.New("channel_type不能为空！")
	// ErrMessageEventClientMsgNoRequired reports that a message event append has no client message number.
	ErrMessageEventClientMsgNoRequired = errors.New("client_msg_no不能为空！")
	// ErrMessageEventIDRequired reports that a message event append has no event idempotency key.
	ErrMessageEventIDRequired = errors.New("event_id不能为空！")
	// ErrMessageEventTypeRequired reports that a message event append has no event type.
	ErrMessageEventTypeRequired = errors.New("event_type不能为空！")
	// ErrSendHookDepthExceeded reports that a plugin-origin send exceeded hook recursion limits.
	ErrSendHookDepthExceeded = errors.New("internal/message: send hook depth exceeded")
	// ErrRequestSubscribersRequireSyncOnce reports that request-scoped sends must be sync_once.
	ErrRequestSubscribersRequireSyncOnce = channelappend.ErrRequestSubscribersRequireSyncOnce
	// ErrRequestSubscribersConflictChannel reports that request-scoped sends cannot specify a channel.
	ErrRequestSubscribersConflictChannel = channelappend.ErrRequestSubscribersConflictChannel
	// ErrRequestSubscribersRequired reports that request-scoped sends need at least one usable subscriber.
	ErrRequestSubscribersRequired = channelappend.ErrRequestSubscribersRequired
	// ErrAppenderRequired reports that durable append is not configured.
	ErrAppenderRequired = channelappend.ErrAppenderRequired
	// ErrMessageIDAllocatorRequired reports that message id allocation is not configured.
	ErrMessageIDAllocatorRequired = channelappend.ErrMessageIDAllocatorRequired
	// ErrNotLeader reports that the append target is no longer the leader.
	ErrNotLeader = channelappend.ErrNotLeader
	// ErrNotChannelAuthority reports that the local node is not the channel authority.
	ErrNotChannelAuthority = channelappend.ErrNotChannelAuthority
	// ErrStaleRoute reports that append used stale channel metadata.
	ErrStaleRoute = channelappend.ErrStaleRoute
	// ErrRouteNotReady reports that cluster routing is not ready for foreground writes.
	ErrRouteNotReady = channelappend.ErrRouteNotReady
	// ErrChannelNotFound reports that the target channel is not available.
	ErrChannelNotFound = channelappend.ErrChannelNotFound
	// ErrBackpressured reports bounded runtime pressure.
	ErrBackpressured = channelappend.ErrBackpressured
	// ErrChannelBusy reports that channel-level write flow control is saturated.
	ErrChannelBusy = channelappend.ErrChannelBusy
	// ErrAppendFailed wraps unexpected append failures.
	ErrAppendFailed = channelappend.ErrAppendFailed
	// ErrAppendResultMissing reports a successful batch append response without a matching item result.
	ErrAppendResultMissing = channelappend.ErrAppendResultMissing
)
