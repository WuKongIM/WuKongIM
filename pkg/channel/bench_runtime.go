package channel

import (
	"context"
	"time"
)

// RuntimeBench exposes benchmark-only runtime observation and cleanup.
type RuntimeBench interface {
	RuntimeSnapshot(context.Context) (RuntimeSnapshot, error)
	RuntimeProbe(context.Context, RuntimeSelector) (RuntimeProbeResult, error)
	RuntimeEvict(context.Context, RuntimeSelector) (RuntimeEvictResult, error)
}

// RuntimeDrain exposes migration-owned local leader drain checks.
type RuntimeDrain interface {
	DrainChannel(context.Context, DrainChannelRequest) (DrainChannelResult, error)
}

// RuntimeSelector selects concrete channel identities. Callers expand benchmark ranges before entering pkg/channelv2.
type RuntimeSelector struct {
	// ChannelIDs are copied by callers before asynchronous use.
	ChannelIDs []ChannelID
}

// RuntimeSnapshot is one node's low-cardinality ChannelV2 runtime view.
type RuntimeSnapshot struct {
	// NodeID is the local node that produced this snapshot.
	NodeID NodeID
	// ActiveTotal is the number of all loaded local runtimes.
	ActiveTotal int
	// ActiveLeader is the number of loaded local runtimes in leader role.
	ActiveLeader int
	// ActiveFollower is the number of loaded local runtimes in follower role.
	ActiveFollower int
	// FollowerParked is the number of loaded follower runtimes parked by replication state.
	FollowerParked int
	// ActivationRejectedTotal is the local activation rejections observed by the runtime.
	ActivationRejectedTotal uint64
	// Reactors contains per-reactor runtime summaries.
	Reactors []RuntimeReactorSnapshot
	// WorkerQueues contains bounded worker pool queue summaries.
	WorkerQueues []RuntimeWorkerQueue
}

// RuntimeReactorSnapshot summarizes one reactor partition.
type RuntimeReactorSnapshot struct {
	// ReactorID identifies the reactor partition.
	ReactorID int
	// Leader is the number of loaded leader runtimes owned by this reactor.
	Leader int
	// Follower is the number of loaded follower runtimes owned by this reactor.
	Follower int
	// Parked is the number of parked follower runtimes owned by this reactor.
	Parked int
	// MailboxDepth is the pending mailbox depth for this reactor.
	MailboxDepth int
}

// RuntimeWorkerQueue summarizes one bounded worker pool queue.
type RuntimeWorkerQueue struct {
	// Pool is the worker pool name.
	Pool string
	// Depth is the current queued task depth.
	Depth int
}

// RuntimeProbeResult reports local loaded runtime presence for selected channels.
type RuntimeProbeResult struct {
	// Checked is the number of selected channels inspected.
	Checked int
	// LoadedLeader is the number of selected channels loaded locally as leaders.
	LoadedLeader int
	// LoadedFollower is the number of selected channels loaded locally as followers.
	LoadedFollower int
	// Channels contains detailed proof fields for loaded selected channels.
	Channels []RuntimeProbeChannel
	// Missing contains selected channels that are not loaded locally.
	Missing []ChannelID
}

// RuntimeProbeChannel reports local migration proof fields for one loaded channel runtime.
type RuntimeProbeChannel struct {
	// ChannelID identifies the loaded channel runtime.
	ChannelID ChannelID
	// LeaderEpoch is the local runtime's current leader epoch.
	LeaderEpoch uint64
	// ChannelEpoch is the local runtime's current membership epoch.
	ChannelEpoch uint64
	// Role is the local runtime role.
	Role Role
	// Status is the local runtime status.
	Status Status
	// LEO is the local log end offset.
	LEO uint64
	// HW is the local committed high watermark.
	HW uint64
	// CheckpointHW is the local durable checkpoint high watermark.
	CheckpointHW uint64
	// WriteFence is the currently applied durable write fence.
	WriteFence WriteFence
	// InflightAppend reports whether a durable append batch is waiting on store completion.
	InflightAppend bool
	// PendingAppendCount is the number of admitted append waiters not yet completed.
	PendingAppendCount int
}

// RuntimeEvictResult reports local runtime eviction results.
type RuntimeEvictResult struct {
	// Requested is the number of selected channels requested for eviction.
	Requested int
	// Evicted is the number of loaded runtimes evicted locally.
	Evicted int
	// SkippedBusy is the number of loaded runtimes not evicted because they were not safe.
	SkippedBusy int
	// Missing is the number of selected channels that were not loaded locally.
	Missing int
}

// DrainChannelRequest asks a local leader runtime to prove all accepted appends are committed under a fence.
type DrainChannelRequest struct {
	// ChannelID identifies the channel runtime to drain.
	ChannelID ChannelID
	// LeaderEpoch fences the drain against leader changes.
	LeaderEpoch uint64
	// FenceVersion is the expected write-fence version owned by the migration task.
	FenceVersion uint64
	// Timeout bounds polling for pending accepted appends to drain. Zero performs one check.
	Timeout time.Duration
}

// DrainChannelResult reports one local drain check result.
type DrainChannelResult struct {
	// Drained reports no accepted append remains and local HW covers local LEO.
	Drained bool
	// LEO is the observed local log end offset.
	LEO uint64
	// HW is the observed local high watermark.
	HW uint64
}
