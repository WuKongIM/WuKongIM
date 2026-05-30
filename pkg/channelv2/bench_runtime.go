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
	NodeID                  NodeID
	ActiveTotal             int
	ActiveLeader            int
	ActiveFollower          int
	FollowerParked          int
	ActivationRejectedTotal uint64
	Reactors                []RuntimeReactorSnapshot
	WorkerQueues            []RuntimeWorkerQueue
}

// RuntimeReactorSnapshot summarizes one reactor partition.
type RuntimeReactorSnapshot struct {
	ReactorID    int
	Leader       int
	Follower     int
	Parked       int
	MailboxDepth int
}

// RuntimeWorkerQueue summarizes one bounded worker pool queue.
type RuntimeWorkerQueue struct {
	Pool  string
	Depth int
}

// RuntimeProbeResult reports local loaded runtime presence for selected channels.
type RuntimeProbeResult struct {
	Checked        int
	LoadedLeader   int
	LoadedFollower int
	Missing        []ChannelID
}

// RuntimeEvictResult reports local runtime eviction results.
type RuntimeEvictResult struct {
	Requested   int
	Evicted     int
	SkippedBusy int
	Missing     int
}
