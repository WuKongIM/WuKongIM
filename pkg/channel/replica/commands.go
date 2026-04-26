package replica

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

type machineCommand interface {
	machineEvent
	isMachineCommand()
}

type machineEvent interface {
	isMachineEvent()
}

type machineEffect interface {
	isMachineEffect()
}

type machineAppendCommand struct {
	RequestID uint64
	Records   []channel.Record
	Now       time.Time
}

func (machineAppendCommand) isMachineEvent()   {}
func (machineAppendCommand) isMachineCommand() {}

type machineAppendRequestCommand struct {
	// Request is owned by the loop until exactly one completion is sent.
	Request *appendRequest
	// Now is the facade-observed time used for appendability checks.
	Now time.Time
}

func (machineAppendRequestCommand) isMachineEvent()   {}
func (machineAppendRequestCommand) isMachineCommand() {}

type machineAppendCancelCommand struct {
	// Request identifies the queued, in-flight, or waiting append to complete.
	Request *appendRequest
	// Err is the caller cancellation error reported to the append waiter.
	Err error
}

func (machineAppendCancelCommand) isMachineEvent()   {}
func (machineAppendCancelCommand) isMachineCommand() {}

type machineAppendFlushEvent struct{}

func (machineAppendFlushEvent) isMachineEvent() {}

type machineCursorCommand struct {
	ChannelKey  channel.ChannelKey
	Epoch       uint64
	ReplicaID   channel.NodeID
	MatchOffset uint64
	OffsetEpoch uint64
}

func (machineCursorCommand) isMachineEvent()   {}
func (machineCursorCommand) isMachineCommand() {}

type machineFetchProgressCommand struct {
	// Request is the follower fetch envelope whose cursor should be applied by the loop.
	Request channel.ReplicaFetchRequest
}

func (machineFetchProgressCommand) isMachineEvent()   {}
func (machineFetchProgressCommand) isMachineCommand() {}

type machineReadLogResultCommand struct {
	// Effect is the read-log fence captured by the loop before storage I/O.
	Effect readLogEffect
	// Records are the raw records returned by LogStore.Read before loop clipping.
	Records []channel.Record
	// Err is the read error, if storage failed before the result returned to the loop.
	Err error
}

func (machineReadLogResultCommand) isMachineEvent()   {}
func (machineReadLogResultCommand) isMachineCommand() {}

type machineReconcileProofCommand struct {
	// Proof is a peer tail proof used to update leader progress during reconcile.
	Proof channel.ReplicaReconcileProof
}

func (machineReconcileProofCommand) isMachineEvent()   {}
func (machineReconcileProofCommand) isMachineCommand() {}

type machineCompleteReconcileCommand struct {
	// Meta fences local reconcile completion to the leader epoch that started it.
	Meta channel.Meta
}

func (machineCompleteReconcileCommand) isMachineEvent()   {}
func (machineCompleteReconcileCommand) isMachineCommand() {}

type machineLeaderReconcileResultCommand struct {
	// EffectID fences this durable result to the loop-owned reconcile effect.
	EffectID uint64
	// ChannelKey, Epoch, LeaderEpoch, and RoleGeneration fence the leader term.
	ChannelKey     channel.ChannelKey
	Epoch          uint64
	LeaderEpoch    uint64
	RoleGeneration uint64
	StoredLEO      uint64
	HW             uint64
	Checkpoint     channel.Checkpoint
	Truncated      bool
	TruncateTo     *uint64
	Err            error
}

func (machineLeaderReconcileResultCommand) isMachineEvent()   {}
func (machineLeaderReconcileResultCommand) isMachineCommand() {}

type machineTombstoneCommand struct{}

func (machineTombstoneCommand) isMachineEvent()   {}
func (machineTombstoneCommand) isMachineCommand() {}

type machineCloseCommand struct{}

func (machineCloseCommand) isMachineEvent()   {}
func (machineCloseCommand) isMachineCommand() {}

type machineApplyMetaCommand struct {
	Meta channel.Meta
}

func (machineApplyMetaCommand) isMachineEvent()   {}
func (machineApplyMetaCommand) isMachineCommand() {}

type machineBecomeLeaderCommand struct {
	Meta channel.Meta
}

func (machineBecomeLeaderCommand) isMachineEvent()   {}
func (machineBecomeLeaderCommand) isMachineCommand() {}

type machineBeginLeaderEpochResultCommand struct {
	// EffectID fences this durable epoch-boundary result to the leader command.
	EffectID uint64
	// Meta is the validated leader metadata captured before durable I/O.
	Meta channel.Meta
	// RoleGeneration fences against lifecycle/meta changes while durable I/O runs.
	RoleGeneration uint64
	LEO            uint64
	EpochPoint     channel.EpochPoint
	Err            error
}

func (machineBeginLeaderEpochResultCommand) isMachineEvent()   {}
func (machineBeginLeaderEpochResultCommand) isMachineCommand() {}

type machineBecomeFollowerCommand struct {
	Meta channel.Meta
}

func (machineBecomeFollowerCommand) isMachineEvent()   {}
func (machineBecomeFollowerCommand) isMachineCommand() {}

type machineInstallSnapshotCommand struct {
	Snapshot channel.Snapshot
}

func (machineInstallSnapshotCommand) isMachineEvent()   {}
func (machineInstallSnapshotCommand) isMachineCommand() {}

type machineApplyFetchCommand struct {
	// Request is the leader batch or heartbeat to apply on a follower.
	Request channel.ReplicaApplyFetchRequest
}

func (machineApplyFetchCommand) isMachineEvent()   {}
func (machineApplyFetchCommand) isMachineCommand() {}

type machineFollowerApplyResultCommand struct {
	// EffectID fences this durable result to the loop-owned apply effect.
	EffectID uint64
	// ChannelKey, Epoch, Leader, and RoleGeneration fence the request metadata.
	ChannelKey     channel.ChannelKey
	Epoch          uint64
	Leader         channel.NodeID
	RoleGeneration uint64
	// StoredLEO is the durable LEO returned by the apply effect.
	StoredLEO   uint64
	NewHW       uint64
	CommitReady bool
	Truncated   bool
	TruncateTo  *uint64
	Checkpoint  *channel.Checkpoint
	EpochPoint  *channel.EpochPoint
	Err         error
}

func (machineFollowerApplyResultCommand) isMachineEvent()   {}
func (machineFollowerApplyResultCommand) isMachineCommand() {}

type machineAdvanceHWEvent struct{}

func (machineAdvanceHWEvent) isMachineEvent() {}

type machineLeaderAppendCommittedEvent struct {
	EffectID       uint64
	ChannelKey     channel.ChannelKey
	Epoch          uint64
	LeaderEpoch    uint64
	RoleGeneration uint64
	LeaseUntil     time.Time
	// RequestIDs are the loop-owned append requests covered by the durable batch.
	RequestIDs []uint64
	// BaseOffset is the log offset returned by the durable append before the batch.
	BaseOffset uint64
	// DurableStartedAt captures when the durable append began for trace timing.
	DurableStartedAt time.Time
	// DoneAt captures when the durable append finished for waiter timing.
	DoneAt time.Time
	Err    error
}

func (machineLeaderAppendCommittedEvent) isMachineEvent() {}

type machineCheckpointStoredEvent struct {
	EffectID       uint64
	ChannelKey     channel.ChannelKey
	Epoch          uint64
	LeaderEpoch    uint64
	RoleGeneration uint64
	Checkpoint     channel.Checkpoint
	Err            error
}

func (machineCheckpointStoredEvent) isMachineEvent() {}

type machineCheckpointRetryEvent struct{}

func (machineCheckpointRetryEvent) isMachineEvent() {}

type machineSnapshotInstalledEvent struct {
	EffectID       uint64
	ChannelKey     channel.ChannelKey
	Epoch          uint64
	RoleGeneration uint64
	View           durableView
	Committed      bool
	Err            error
}

func (machineSnapshotInstalledEvent) isMachineEvent() {}

type appendLeaderBatchEffect struct {
	EffectID       uint64
	RequestIDs     []uint64
	ChannelKey     channel.ChannelKey
	Epoch          uint64
	LeaderEpoch    uint64
	RoleGeneration uint64
	LeaseUntil     time.Time
	Records        []channel.Record
	StartedAt      time.Time
}

func (appendLeaderBatchEffect) isMachineEffect() {}

type readLogEffect struct {
	EffectID       uint64
	ChannelKey     channel.ChannelKey
	Epoch          uint64
	RoleGeneration uint64
	LeaderLEO      uint64
	FetchOffset    uint64
	MaxBytes       int
	Result         channel.ReplicaFetchResult
}

func (readLogEffect) isMachineEffect() {}

type applyFollowerEffect struct {
	EffectID       uint64
	ChannelKey     channel.ChannelKey
	Epoch          uint64
	Leader         channel.NodeID
	RoleGeneration uint64
	TruncateTo     *uint64
	BaseLEO        uint64
	NewLEO         uint64
	PreviousHW     uint64
	NewHW          uint64
	CommitReady    bool
	Records        []channel.Record
	Checkpoint     *channel.Checkpoint
	EpochPoint     *channel.EpochPoint
	StartedAt      time.Time
}

func (applyFollowerEffect) isMachineEffect() {}

type beginLeaderEpochEffect struct {
	EffectID       uint64
	Meta           channel.Meta
	RoleGeneration uint64
	LEO            uint64
	EpochPoint     channel.EpochPoint
}

func (beginLeaderEpochEffect) isMachineEffect() {}

type storeCheckpointEffect struct {
	EffectID       uint64
	ChannelKey     channel.ChannelKey
	Epoch          uint64
	LeaderEpoch    uint64
	RoleGeneration uint64
	Checkpoint     channel.Checkpoint
	VisibleHW      uint64
	LEO            uint64
}

func (storeCheckpointEffect) isMachineEffect() {}

type installSnapshotEffect struct {
	EffectID       uint64
	ChannelKey     channel.ChannelKey
	Epoch          uint64
	RoleGeneration uint64
	Snapshot       channel.Snapshot
	Checkpoint     channel.Checkpoint
	EpochPoint     channel.EpochPoint
}

func (installSnapshotEffect) isMachineEffect() {}

type leaderReconcileEffect struct {
	Meta       channel.Meta
	Local      bool
	Probe      bool
	ProbeStore ReconcileProbeSource
}

func (leaderReconcileEffect) isMachineEffect() {}

type leaderReconcileDurableEffect struct {
	EffectID       uint64
	ChannelKey     channel.ChannelKey
	Epoch          uint64
	LeaderEpoch    uint64
	RoleGeneration uint64
	TruncateTo     *uint64
	NewLEO         uint64
	HW             uint64
	Checkpoint     channel.Checkpoint
}

func (leaderReconcileDurableEffect) isMachineEffect() {}

type publishStateEffect struct {
	State channel.ReplicaState
}

func (publishStateEffect) isMachineEffect() {}

type completeAppendEffect struct {
	Err error
}

func (completeAppendEffect) isMachineEffect() {}
