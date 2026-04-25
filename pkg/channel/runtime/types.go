package runtime

import (
	"context"
	"errors"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var (
	ErrInvalidConfig      = core.ErrInvalidConfig
	ErrChannelNotFound    = core.ErrChannelNotFound
	ErrChannelExists      = errors.New("runtime: channel already exists")
	ErrTooManyChannels    = errors.New("runtime: too many channels")
	ErrGenerationMismatch = errors.New("runtime: generation mismatch")
	ErrBackpressured      = errors.New("runtime: backpressured")
)

type TombstonePolicy struct {
	TombstoneTTL    time.Duration
	CleanupInterval time.Duration
}

type MessageKind uint8

const (
	MessageKindFetchRequest MessageKind = iota + 1
	MessageKindFetchResponse
	MessageKindFetchFailure
	MessageKindReconcileProbeRequest
	MessageKindReconcileProbeResponse
	MessageKindTruncate
	MessageKindSnapshotChunk
	MessageKindAck
	MessageKindLanePollRequest
	MessageKindLanePollResponse
)

type BackpressureLevel uint8

const (
	BackpressureNone BackpressureLevel = iota
	BackpressureSoft
	BackpressureHard
)

type BackpressureState struct {
	Level           BackpressureLevel
	PendingRequests int
	PendingBytes    int64
}

type Envelope struct {
	Peer       core.NodeID
	ChannelKey core.ChannelKey
	Epoch      uint64
	Generation uint64
	RequestID  uint64
	Kind       MessageKind
	Sync       bool
	Payload    []byte

	FetchRequest           *FetchRequestEnvelope
	FetchResponse          *FetchResponseEnvelope
	ReconcileProbeRequest  *ReconcileProbeRequestEnvelope
	ReconcileProbeResponse *ReconcileProbeResponseEnvelope
	LanePollRequest        *LanePollRequestEnvelope
	LanePollResponse       *LanePollResponseEnvelope
}

type FetchRequestEnvelope struct {
	ChannelKey  core.ChannelKey
	Epoch       uint64
	Generation  uint64
	ReplicaID   core.NodeID
	FetchOffset uint64
	OffsetEpoch uint64
	MaxBytes    int
}

type FetchResponseEnvelope struct {
	ChannelKey core.ChannelKey
	Epoch      uint64
	Generation uint64
	TruncateTo *uint64
	LeaderHW   uint64
	Records    []core.Record
}

type ReconcileProbeRequestEnvelope struct {
	ChannelKey core.ChannelKey
	Epoch      uint64
	Generation uint64
	ReplicaID  core.NodeID
}

type ReconcileProbeResponseEnvelope struct {
	ChannelKey   core.ChannelKey
	Epoch        uint64
	Generation   uint64
	ReplicaID    core.NodeID
	OffsetEpoch  uint64
	LogEndOffset uint64
	CheckpointHW uint64
}

type PeerLaneKey struct {
	Peer   core.NodeID
	LaneID uint16
}

type LaneCursorDelta struct {
	ChannelKey   core.ChannelKey
	ChannelEpoch uint64
	MatchOffset  uint64
	OffsetEpoch  uint64
}

type LanePollBudget struct {
	MaxBytes    int
	MaxChannels int
}

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

type LanePollResetReason uint8

const (
	LanePollResetReasonNone LanePollResetReason = iota
	LanePollResetReasonLaneLayoutMismatch
	LanePollResetReasonSessionEpochMismatch
	LanePollResetReasonMembershipVersionMismatch
	LanePollResetReasonLeaderRestart
	LanePollResetReasonSessionEvicted
)

type LanePollItemFlags uint8

const (
	LanePollItemFlagData LanePollItemFlags = 1 << iota
	LanePollItemFlagTruncate
	LanePollItemFlagHWOnly
	LanePollItemFlagReset
)

type LaneMembership struct {
	ChannelKey   core.ChannelKey
	ChannelEpoch uint64
}

type LaneResponseItem struct {
	ChannelKey   core.ChannelKey
	ChannelEpoch uint64
	LeaderEpoch  uint64
	Flags        LanePollItemFlags
	Records      []core.Record
	LeaderHW     uint64
	TruncateTo   *uint64
}

type LanePollRequestEnvelope struct {
	ReplicaID             core.NodeID
	LaneID                uint16
	LaneCount             uint16
	SessionID             uint64
	SessionEpoch          uint64
	Op                    LanePollOp
	ProtocolVersion       uint16
	MaxWait               time.Duration
	MaxBytes              int
	MaxChannels           int
	MembershipVersionHint uint64
	FullMembership        []LaneMembership
	CursorDelta           []LaneCursorDelta
}

type LanePollResponseEnvelope struct {
	LaneID        uint16
	Status        LanePollStatus
	SessionID     uint64
	SessionEpoch  uint64
	TimedOut      bool
	MoreReady     bool
	ResetRequired bool
	ResetReason   LanePollResetReason
	Items         []LaneResponseItem
}

type LanePollService interface {
	ServeLanePoll(ctx context.Context, req LanePollRequestEnvelope) (LanePollResponseEnvelope, error)
}

type Limits struct {
	MaxChannels               int
	MaxFetchInflightPeer      int
	MaxSnapshotInflight       int
	MaxRecoveryBytesPerSecond int64
}

type Runtime interface {
	FetchService
	EnsureChannel(meta core.Meta) error
	RemoveChannel(key core.ChannelKey) error
	ApplyMeta(meta core.Meta) error
	Channel(key core.ChannelKey) (ChannelHandle, bool)
	Close() error
}

type FetchService interface {
	ServeFetch(ctx context.Context, req FetchRequestEnvelope) (FetchResponseEnvelope, error)
}

type ReconcileProbeService interface {
	ServeReconcileProbe(ctx context.Context, req ReconcileProbeRequestEnvelope) (ReconcileProbeResponseEnvelope, error)
}

type ChannelHandle = core.HandlerChannel

type ChannelConfig struct {
	ChannelKey           core.ChannelKey
	Generation           uint64
	Meta                 core.Meta
	OnReplicaStateChange func()
}

type ReplicaFactory interface {
	New(cfg ChannelConfig) (replica.Replica, error)
}

type GenerationStore interface {
	Load(key core.ChannelKey) (uint64, error)
	Store(key core.ChannelKey, generation uint64) error
}

type Transport interface {
	Send(peer core.NodeID, env Envelope) error
	RegisterHandler(fn func(Envelope))
}

type PeerSessionManager interface {
	Session(peer core.NodeID) PeerSession
}

type PeerSession interface {
	Send(env Envelope) error
	Backpressure() BackpressureState
	Close() error
}

type Config struct {
	LocalNode                        core.NodeID
	ReplicaFactory                   ReplicaFactory
	GenerationStore                  GenerationStore
	Activator                        Activator
	Transport                        Transport
	PeerSessions                     PeerSessionManager
	AutoRunScheduler                 bool
	FollowerReplicationRetryInterval time.Duration
	LongPollLaneCount                int
	LongPollMaxWait                  time.Duration
	LongPollMaxBytes                 int
	LongPollMaxChannels              int
	Tombstones                       TombstonePolicy
	Limits                           Limits
	Now                              func() time.Time
	Logger                           wklog.Logger
}
