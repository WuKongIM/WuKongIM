package message

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelwrite"
)

var (
	// ErrInvalidCommand reports a malformed send command.
	ErrInvalidCommand = channelwrite.ErrInvalidCommand
	// ErrSyncLoginUIDRequired reports that a legacy message sync request has no login UID.
	ErrSyncLoginUIDRequired = errors.New("login_uid不能为空！")
	// ErrSyncChannelIDRequired reports that a legacy message sync request has no channel ID.
	ErrSyncChannelIDRequired = errors.New("channel_id不能为空！")
	// ErrSyncChannelTypeRequired reports that a legacy message sync request has no channel type.
	ErrSyncChannelTypeRequired = errors.New("channel_type不能为空！")
	// ErrMessageReaderRequired reports that channel message sync is not configured.
	ErrMessageReaderRequired = errors.New("internalv2/message: message reader required")
	// ErrRequestSubscribersRequireSyncOnce reports that request-scoped sends must be sync_once.
	ErrRequestSubscribersRequireSyncOnce = channelwrite.ErrRequestSubscribersRequireSyncOnce
	// ErrRequestSubscribersConflictChannel reports that request-scoped sends cannot specify a channel.
	ErrRequestSubscribersConflictChannel = channelwrite.ErrRequestSubscribersConflictChannel
	// ErrRequestSubscribersRequired reports that request-scoped sends need at least one usable subscriber.
	ErrRequestSubscribersRequired = channelwrite.ErrRequestSubscribersRequired
	// ErrAppenderRequired reports that durable append is not configured.
	ErrAppenderRequired = channelwrite.ErrAppenderRequired
	// ErrMessageIDAllocatorRequired reports that message id allocation is not configured.
	ErrMessageIDAllocatorRequired = channelwrite.ErrMessageIDAllocatorRequired
	// ErrNotLeader reports that the append target is no longer the leader.
	ErrNotLeader = channelwrite.ErrNotLeader
	// ErrNotChannelAuthority reports that the local node is not the channel authority.
	ErrNotChannelAuthority = channelwrite.ErrNotChannelAuthority
	// ErrStaleRoute reports that append used stale channel metadata.
	ErrStaleRoute = channelwrite.ErrStaleRoute
	// ErrRouteNotReady reports that cluster routing is not ready for foreground writes.
	ErrRouteNotReady = channelwrite.ErrRouteNotReady
	// ErrChannelNotFound reports that the target channel is not available.
	ErrChannelNotFound = channelwrite.ErrChannelNotFound
	// ErrBackpressured reports bounded runtime pressure.
	ErrBackpressured = channelwrite.ErrBackpressured
	// ErrChannelBusy reports that channel-level write flow control is saturated.
	ErrChannelBusy = channelwrite.ErrChannelBusy
	// ErrAppendFailed wraps unexpected append failures.
	ErrAppendFailed = channelwrite.ErrAppendFailed
	// ErrAppendResultMissing reports a successful batch append response without a matching item result.
	ErrAppendResultMissing = channelwrite.ErrAppendResultMissing
)
