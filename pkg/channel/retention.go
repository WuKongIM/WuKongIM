package channel

import "context"

const (
	// RetentionCursorCommitted names the logical cursor adopted from committed control-plane state.
	RetentionCursorCommitted = "committed"

	// RetentionBlockedHWLag means the local committed frontier does not cover the requested trim.
	RetentionBlockedHWLag = "hw_lag"
	// RetentionBlockedCheckpointLag means the durable checkpoint does not cover the requested trim.
	RetentionBlockedCheckpointLag = "checkpoint_lag"
	// RetentionBlockedLEOLag means the local log end offset does not cover the requested trim.
	RetentionBlockedLEOLag = "leo_lag"
	// RetentionBlockedMinISRLag means a leader still has an ISR replica behind the requested trim.
	RetentionBlockedMinISRLag = "min_isr_lag"
)

// RetentionRuntime exposes local Channel log-compaction views and apply operations.
type RetentionRuntime interface {
	RetentionView(context.Context, ChannelID) (RetentionView, error)
	ApplyRetentionBoundary(context.Context, RetentionApplyRequest) (RetentionApplyResult, error)
}

// RetentionView reports one local runtime's logical and physical retention progress.
type RetentionView struct {
	// Key is the channel runtime key used for reactor routing.
	Key ChannelKey
	// ChannelID is the client-visible channel identity.
	ChannelID ChannelID
	// Role is the local role under the current metadata fence.
	Role Role
	// Leader is the authoritative leader node.
	Leader NodeID
	// Replicas are the current channel replicas.
	Replicas []NodeID
	// ISR are replicas that participate in HW and leader retention safety.
	ISR []NodeID
	// RetentionThroughSeq is the authoritative logical compaction boundary.
	RetentionThroughSeq uint64
	// LocalRetentionThroughSeq is the local store-adopted logical boundary.
	LocalRetentionThroughSeq uint64
	// PhysicalRetentionThroughSeq is the highest locally deleted message sequence.
	PhysicalRetentionThroughSeq uint64
	// LEO is the local log end offset.
	LEO uint64
	// HW is the local committed high watermark.
	HW uint64
	// CheckpointHW is the durable committed checkpoint.
	CheckpointHW uint64
	// MinISRMatchOffset is the lowest known ISR match offset for leader trim safety.
	MinISRMatchOffset uint64
}

// RetentionApplyOptions bounds one physical trim attempt after adopting a boundary.
type RetentionApplyOptions struct {
	// MaxTrimMessages caps deleted messages when positive.
	MaxTrimMessages int
	// MaxTrimBytes caps deleted payload bytes when positive.
	MaxTrimBytes int
}

// RetentionApplyRequest asks the local runtime to adopt and optionally trim a boundary.
type RetentionApplyRequest struct {
	// ChannelID identifies the channel store and runtime.
	ChannelID ChannelID
	// ThroughSeq is the inclusive logical compaction boundary.
	ThroughSeq uint64
	// Options bounds the physical trim performed by this request.
	Options RetentionApplyOptions
}

// RetentionApplyResult describes one local retention apply attempt.
type RetentionApplyResult struct {
	// ChannelID identifies the channel that was processed.
	ChannelID ChannelID
	// ThroughSeq is the requested inclusive compaction boundary.
	ThroughSeq uint64
	// LocalRetentionThroughSeq is the local store-adopted boundary after the task.
	LocalRetentionThroughSeq uint64
	// PhysicalRetentionThroughSeq is the highest locally deleted sequence after the task.
	PhysicalRetentionThroughSeq uint64
	// DeletedThroughSeq is the highest sequence deleted by this task.
	DeletedThroughSeq uint64
	// Deleted is the number of messages physically removed by this task.
	Deleted int
	// More reports whether more rows may still be removable below ThroughSeq.
	More bool
	// BlockedReason is non-empty when safety checks allowed adoption but blocked physical trim.
	BlockedReason string
}
