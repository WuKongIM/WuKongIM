package transport

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
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
	ChannelKey   channel.ChannelKey
	ChannelEpoch uint64
}

type LongPollCursorDelta struct {
	ChannelKey   channel.ChannelKey
	ChannelEpoch uint64
	MatchOffset  uint64
	OffsetEpoch  uint64
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
	ChannelKey   channel.ChannelKey
	ChannelEpoch uint64
	LeaderEpoch  uint64
	Flags        LongPollItemFlags
	Records      []channel.Record
	LeaderHW     uint64
	TruncateTo   *uint64
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
