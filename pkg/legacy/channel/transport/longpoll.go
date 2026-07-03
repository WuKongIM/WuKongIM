package transport

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

type LanePollOp uint8

const (
	LanePollOpOpen LanePollOp = iota + 1
	LanePollOpPoll
	LanePollOpClose
)

type LanePollStatus uint8

const (
	LanePollStatusOK LanePollStatus = iota + 1
	LanePollStatusNeedReset
	LanePollStatusStaleMeta
	LanePollStatusNotLeader
	LanePollStatusClosed
)

type LongPollCapability uint8

const (
	LongPollCapabilityQuorumAck LongPollCapability = 1 << iota
	LongPollCapabilityLocalAck
)

type LongPollResetReason uint8

const (
	LongPollResetReasonNone LongPollResetReason = iota
	LongPollResetReasonLaneLayoutMismatch
	LongPollResetReasonSessionEpochMismatch
	LongPollResetReasonMembershipVersionMismatch
	LongPollResetReasonLeaderRestart
	LongPollResetReasonSessionEvicted
)

type LongPollItemFlags uint8

const (
	LongPollItemFlagData LongPollItemFlags = 1 << iota
	LongPollItemFlagTruncate
	LongPollItemFlagHWOnly
	LongPollItemFlagReset
)

type LongPollMembership struct {
	ChannelKey channel.ChannelKey
	// ChannelEpoch is the authoritative channel epoch for this long-poll member.
	ChannelEpoch uint64
	// ChannelGeneration is the follower-local channel generation echoed by leader items.
	ChannelGeneration uint64
}

type LongPollCursorDelta struct {
	ChannelKey channel.ChannelKey
	// ChannelEpoch fences this cursor update to the matching channel epoch.
	ChannelEpoch uint64
	// ChannelGeneration fences this cursor update to the follower-local channel generation.
	ChannelGeneration uint64
	MatchOffset       uint64
	OffsetEpoch       uint64
}

type LongPollFetchRequest struct {
	PeerID                channel.NodeID
	LaneID                uint16
	LaneCount             uint16
	SessionID             uint64
	SessionEpoch          uint64
	Op                    LanePollOp
	ProtocolVersion       uint16
	Capabilities          LongPollCapability
	MaxWaitMs             uint32
	MaxBytes              uint32
	MaxChannels           uint32
	MembershipVersionHint uint64
	FullMembership        []LongPollMembership
	CursorDelta           []LongPollCursorDelta
}

type LongPollItem struct {
	ChannelKey channel.ChannelKey
	// ChannelEpoch preserves the existing cluster epoch fence for this response item.
	ChannelEpoch uint64
	// ChannelGeneration echoes the follower-local generation from the request that produced this item.
	ChannelGeneration uint64
	LeaderEpoch       uint64
	Flags             LongPollItemFlags
	Records           []channel.Record
	LeaderHW          uint64
	TruncateTo        *uint64
	// RetentionReset carries a retained-away prefix that the follower must adopt.
	RetentionReset *channel.RetentionReset
}

type LongPollFetchResponse struct {
	Status        LanePollStatus
	SessionID     uint64
	SessionEpoch  uint64
	TimedOut      bool
	MoreReady     bool
	ResetRequired bool
	ResetReason   LongPollResetReason
	Items         []LongPollItem
}

type LongPollService interface {
	ServeLongPollFetch(ctx context.Context, req LongPollFetchRequest) (LongPollFetchResponse, error)
}

func longPollRPCShardKey(laneID uint16) uint64 {
	return uint64(laneID)
}
