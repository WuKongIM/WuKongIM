package channelv2

import "context"

// RuntimeBench exposes benchmark-only runtime observation and cleanup.
type RuntimeBench interface {
	RuntimeSnapshot(context.Context) (RuntimeSnapshot, error)
	RuntimeProbe(context.Context, RuntimeSelector) (RuntimeProbeResult, error)
	RuntimeEvict(context.Context, RuntimeSelector) (RuntimeEvictResult, error)
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
	// Missing contains selected channels that are not loaded locally.
	Missing []ChannelID
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
