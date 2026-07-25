package backup

import backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"

// CaptureState identifies one Slot worker's current continuous-capture phase.
type CaptureState string

const (
	// CaptureStateIdle means the last observed source cut is fully reconciled.
	CaptureStateIdle CaptureState = "idle"
	// CaptureStateReconciling means the worker is comparing durable and source positions.
	CaptureStateReconciling CaptureState = "reconciling"
	// CaptureStateCapturing means the worker is building or committing segments.
	CaptureStateCapturing CaptureState = "capturing"
	// CaptureStateDegraded means capture is paused behind a repairable dependency failure.
	CaptureStateDegraded CaptureState = "degraded"
	// CaptureStateFailed means the last reconciliation failed.
	CaptureStateFailed CaptureState = "failed"
)

// StreamFrontier is the bounded durable head of one Slot capture stream.
type StreamFrontier struct {
	// Sequence is the latest committed segment sequence in Generation.
	Sequence uint64
	// Head authenticates the latest committed segment; nil means the stream has emitted no data.
	Head *backupartifact.SegmentReference
	// CursorHead authenticates the latest cursor-only sidecar for the message stream.
	CursorHead *backupartifact.SegmentReference
	// SourceCursor is the bounded opaque cursor for authoritative paged reconciliation.
	SourceCursor string
	// SourceHighWatermark is the greatest authoritative position fully reconciled.
	SourceHighWatermark uint64
	// WatermarkAtUnixMillis is the UTC source time represented by SourceHighWatermark.
	WatermarkAtUnixMillis int64
}

// SlotFrontier atomically binds the metadata and message stream heads for one Hash Slot.
type SlotFrontier struct {
	// Revision fences compare-and-swap updates to this compact record.
	Revision uint64
	// HashSlot identifies the logical cluster partition.
	HashSlot uint16
	// Generation identifies the independently replaceable Slot segment graph.
	Generation string
	// Metadata and Messages are independently ordered streams committed together.
	Metadata StreamFrontier
	Messages StreamFrontier
	// WatermarkAtUnixMillis is the older of the two fully reconciled stream times.
	WatermarkAtUnixMillis int64
	// UpdatedAtUnixMillis is the UTC time of the last atomic frontier update.
	UpdatedAtUnixMillis int64
}

// SlotCaptureStatus is one bounded public observation for a Hash Slot.
type SlotCaptureStatus struct {
	// HashSlot identifies the reported logical partition.
	HashSlot uint16
	// State is idle, reconciling, capturing, degraded, or failed.
	State CaptureState
	// Frontier is the last durable atomically committed stream pair.
	Frontier SlotFrontier
	// MetadataSourceWatermark and MessageSourceWatermark are the latest observed positions.
	MetadataSourceWatermark uint64
	MessageSourceWatermark  uint64
	// MetadataLag and MessageLag are source minus durable positions.
	MetadataLag uint64
	MessageLag  uint64
	// ObservedAtUnixMillis is the UTC status observation time.
	ObservedAtUnixMillis int64
	// FailureCategory is a bounded non-sensitive error class.
	FailureCategory string
}

// CloneSlotFrontier returns a detached frontier safe for mutation.
func CloneSlotFrontier(frontier SlotFrontier) SlotFrontier {
	out := frontier
	out.Metadata = cloneStreamFrontier(frontier.Metadata)
	out.Messages = cloneStreamFrontier(frontier.Messages)
	return out
}

// CloneSlotCaptureStatus returns a detached status safe for publication.
func CloneSlotCaptureStatus(status SlotCaptureStatus) SlotCaptureStatus {
	out := status
	out.Frontier = CloneSlotFrontier(status.Frontier)
	return out
}

func cloneStreamFrontier(frontier StreamFrontier) StreamFrontier {
	out := frontier
	if frontier.Head != nil {
		head := *frontier.Head
		out.Head = &head
	}
	if frontier.CursorHead != nil {
		head := *frontier.CursorHead
		out.CursorHead = &head
	}
	return out
}
