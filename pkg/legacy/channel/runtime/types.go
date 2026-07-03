package runtime

import (
	"context"
	"errors"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel/replica"
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
	// RetentionReset tells a follower to adopt a retained-away prefix before fetching more records.
	RetentionReset *core.RetentionReset
}

type ReconcileProbeRequestEnvelope struct {
	ChannelKey core.ChannelKey
	Epoch      uint64
	// LeaderEpoch fences externally triggered probes to the applied leader metadata epoch.
	LeaderEpoch uint64
	Generation  uint64
	ReplicaID   core.NodeID
	// RequireExtendedResponse requests v3 transport fields needed by channel migration proof checks.
	RequireExtendedResponse bool
}

type ReconcileProbeResponseEnvelope struct {
	// ChannelKey identifies the channel runtime that produced this proof.
	ChannelKey core.ChannelKey
	// Epoch is the applied channel membership epoch.
	Epoch uint64
	// LeaderEpoch is the applied leader metadata epoch.
	LeaderEpoch uint64
	// Generation is the node-local runtime generation for this channel.
	Generation uint64
	// ReplicaID is the node that produced this proof.
	ReplicaID core.NodeID
	// Leader is the leader currently applied by this replica.
	Leader core.NodeID
	// Role is the local role currently applied by this replica.
	Role core.ReplicaRole
	// OffsetEpoch owns LogEndOffset when richer epoch history is unavailable.
	OffsetEpoch uint64
	// LogStartOffset is the first local offset available without snapshot bootstrap.
	LogStartOffset uint64
	// LogEndOffset is this replica's durable log end offset.
	LogEndOffset uint64
	// CheckpointHW is this replica's durable checkpoint high watermark.
	CheckpointHW uint64
	// CommitReady reports whether this replica can safely serve its applied role.
	CommitReady bool
}

type PeerLaneKey struct {
	Peer   core.NodeID
	LaneID uint16
}

type LaneCursorDelta struct {
	ChannelKey core.ChannelKey
	// ChannelEpoch fences cursor updates to the channel epoch tracked by the leader lane session.
	ChannelEpoch uint64
	// ChannelGeneration is the follower's local channel generation captured when the cursor was sent.
	ChannelGeneration uint64
	MatchOffset       uint64
	OffsetEpoch       uint64
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
	ChannelKey core.ChannelKey
	// ChannelEpoch is the authoritative channel epoch for this lane membership.
	ChannelEpoch uint64
	// ChannelGeneration is the follower's local channel generation to echo on lane response items.
	ChannelGeneration uint64
}

type LaneResponseItem struct {
	ChannelKey core.ChannelKey
	// ChannelEpoch preserves the existing cluster epoch fence for this lane item.
	ChannelEpoch uint64
	// ChannelGeneration echoes the follower generation from the request that produced this lane item.
	ChannelGeneration uint64
	LeaderEpoch       uint64
	Flags             LanePollItemFlags
	Records           []core.Record
	LeaderHW          uint64
	TruncateTo        *uint64
	// RetentionReset carries the retained-away prefix for this channel lane item.
	RetentionReset *core.RetentionReset
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
	// MaxChannels limits active local channel runtimes on this node. A zero value disables the limit.
	MaxChannels               int
	MaxFetchInflightPeer      int
	MaxSnapshotInflight       int
	MaxRecoveryBytesPerSecond int64
}

type IdleEvictionPolicy struct {
	// IdleTimeout is the idle duration after which a local channel runtime can be evicted.
	// A zero value disables idle eviction and preserves the legacy resident runtime behavior.
	IdleTimeout time.Duration
	// ScanInterval is the interval between idle eviction scans when IdleTimeout is enabled.
	// A zero value uses IdleTimeout, capped to a short operational interval by the runtime.
	ScanInterval time.Duration
}

type Runtime interface {
	FetchService
	EnsureChannel(meta core.Meta) error
	RemoveChannel(key core.ChannelKey) error
	ApplyMeta(meta core.Meta) error
	// FenceAndDrain delegates a migration drain request to the local channel runtime.
	FenceAndDrain(ctx context.Context, req core.FenceAndDrainRequest) (core.DrainResult, error)
	ApplyRetentionBoundary(ctx context.Context, key core.ChannelKey, throughSeq uint64) error
	RetentionView(key core.ChannelKey) (core.RetentionView, error)
	Channel(key core.ChannelKey) (ChannelHandle, bool)
	Close() error
}

type FetchService interface {
	ServeFetch(ctx context.Context, req FetchRequestEnvelope) (FetchResponseEnvelope, error)
}

type ReconcileProbeService interface {
	ServeReconcileProbe(ctx context.Context, req ReconcileProbeRequestEnvelope) (ReconcileProbeResponseEnvelope, error)
}

// MigrationRuntime exposes local migration control operations to channel transport RPCs.
type MigrationRuntime interface {
	// FenceAndDrain validates and drains the local leader for the requested fenced channel.
	FenceAndDrain(ctx context.Context, req core.FenceAndDrainRequest) (core.DrainResult, error)
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
	// LongPollHWOnlyNotifyDelay coalesces commit-only HW wakeups so nearby
	// data responses can carry the newer HW without an extra lane response.
	LongPollHWOnlyNotifyDelay time.Duration
	// LongPollDataNotifyDelay coalesces append wakeups per leader lane so one
	// parked long-poll response can carry multiple ready channels.
	LongPollDataNotifyDelay time.Duration
	Tombstones              TombstonePolicy
	IdleEviction            IdleEvictionPolicy
	Limits                  Limits
	// OnIdleEvict is called after the runtime removes an idle local channel runtime.
	OnIdleEvict func(core.ChannelKey)
	// OnActivationReject is called when the runtime rejects a new local channel activation.
	OnActivationReject func(core.ChannelKey, error)
	Now                func() time.Time
	Logger             wklog.Logger
}
