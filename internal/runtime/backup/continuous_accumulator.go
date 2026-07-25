package backup

import (
	"fmt"
	"sort"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func (e *CaptureEngine) pendingAccumulator(key captureStreamKey, generation string, frontier backupcontract.StreamFrontier) *segmentAccumulator {
	e.pendingMu.Lock()
	accumulator := e.pending[key]
	if accumulator == nil {
		e.pendingMu.Unlock()
		return nil
	}
	if accumulator.matches(generation, frontier) {
		e.pendingMu.Unlock()
		return accumulator
	}
	delete(e.pending, key)
	e.pendingMu.Unlock()
	if accumulator.reservedBytes > 0 {
		e.options.MemoryBudget.Release(accumulator.reservedBytes)
	}
	e.notifyPendingChanged()
	return nil
}

func (e *CaptureEngine) storePendingAccumulator(key captureStreamKey, accumulator *segmentAccumulator) {
	e.pendingMu.Lock()
	previous := e.pending[key]
	e.pending[key] = accumulator
	e.pendingMu.Unlock()
	if previous != nil && previous != accumulator && previous.reservedBytes > 0 {
		e.options.MemoryBudget.Release(previous.reservedBytes)
	}
	e.notifyPendingChanged()
}

func (e *CaptureEngine) removePendingAccumulator(key captureStreamKey, accumulator *segmentAccumulator) {
	e.pendingMu.Lock()
	if e.pending[key] != accumulator {
		e.pendingMu.Unlock()
		return
	}
	delete(e.pending, key)
	reservedBytes := accumulator.reservedBytes
	accumulator.reservedBytes = 0
	e.pendingMu.Unlock()
	if reservedBytes > 0 {
		e.options.MemoryBudget.Release(reservedBytes)
	}
	e.notifyPendingChanged()
}

func (e *CaptureEngine) notifyPendingChanged() {
	select {
	case e.pendingChanged <- struct{}{}:
	default:
	}
}

func (e *CaptureEngine) pendingSchedule(now time.Time) ([]uint16, time.Duration, bool) {
	e.pendingMu.Lock()
	defer e.pendingMu.Unlock()
	dueSet := make(map[uint16]struct{})
	var next time.Duration
	hasNext := false
	for key, accumulator := range e.pending {
		if accumulator == nil || !accumulator.hasRecords() {
			continue
		}
		wait := accumulator.openedAt.Add(e.options.Policy.MaxOpenDuration).Sub(now)
		if wait <= 0 {
			dueSet[key.hashSlot] = struct{}{}
			continue
		}
		if !hasNext || wait < next {
			next = wait
			hasNext = true
		}
	}
	due := make([]uint16, 0, len(dueSet))
	for hashSlot := range dueSet {
		due = append(due, hashSlot)
	}
	sort.Slice(due, func(i, j int) bool { return due[i] < due[j] })
	return due, next, hasNext
}

type captureStreamKey struct {
	hashSlot uint16
	stream   backupartifact.SegmentStream
}

// segmentAccumulator is bounded non-durable rolling state for one Slot stream.
type segmentAccumulator struct {
	// generation and base fence reuse to the exact durable frontier that opened this accumulator.
	generation     string
	baseSequence   uint64
	baseHead       *backupartifact.SegmentReference
	baseCursorHead *backupartifact.SegmentReference
	// fromCursor and nextCursor delimit the source pages currently retained.
	fromCursor string
	nextCursor string
	// openedAt drives sparse-stream time rolling across reconciliation cycles.
	openedAt time.Time
	// target is the greatest authoritative cut represented by retained pages.
	target SourceWatermark
	// scanTarget is the pinned authoritative cut this accumulator is traversing.
	scanTarget SourceWatermark
	// scanDone reports that pages through target were completely examined.
	scanDone bool
	// bytes is encoded record/index size; reservedBytes is its node-budget reservation.
	bytes         int64
	reservedBytes int64
	// records and messageCursor own source-page data after ReadPage returns.
	records       [][]byte
	messageCursor map[channelCursorIdentity]backupartifact.ChannelBoundary
}

type channelCursorIdentity struct {
	channelType uint8
	channelID   string
}

func newSegmentAccumulator(generation string, frontier backupcontract.StreamFrontier, openedAt time.Time) *segmentAccumulator {
	return &segmentAccumulator{
		generation: generation, baseSequence: frontier.Sequence,
		baseHead:       cloneRuntimeSegmentReference(frontier.Head),
		baseCursorHead: cloneRuntimeSegmentReference(frontier.CursorHead),
		fromCursor:     frontier.SourceCursor, nextCursor: frontier.SourceCursor, openedAt: openedAt,
		messageCursor: make(map[channelCursorIdentity]backupartifact.ChannelBoundary),
	}
}

func (a *segmentAccumulator) hasRecords() bool { return len(a.records) > 0 }

func (a *segmentAccumulator) append(page SourcePage, target SourceWatermark, reservation int64) error {
	for _, cursor := range page.MessageCursors {
		identity := channelCursorIdentity{channelType: cursor.ChannelType, channelID: cursor.ChannelID}
		if previous, ok := a.messageCursor[identity]; ok {
			if previous.Epoch != cursor.Epoch || cursor.HW < previous.HW || cursor.LogStartOffset < previous.LogStartOffset {
				return fmt.Errorf("%w: message cursor regressed within segment", ErrSourceRegressed)
			}
		}
	}
	for _, record := range page.Records {
		a.records = append(a.records, record)
		a.bytes += 4 + int64(len(record))
	}
	for _, cursor := range page.MessageCursors {
		identity := channelCursorIdentity{channelType: cursor.ChannelType, channelID: cursor.ChannelID}
		a.messageCursor[identity] = cursor
		a.bytes += int64(len(cursor.ChannelID)) + 32
	}
	a.nextCursor = page.NextCursor
	a.target = SourceWatermark{
		Position: page.NextPosition,
		CommittedAtUnixMillis: olderPositiveTime(
			a.target.CommittedAtUnixMillis,
			target.CommittedAtUnixMillis,
		),
		CutCursor: target.CutCursor,
	}
	a.scanTarget = target
	a.scanDone = page.Done
	a.reservedBytes += reservation
	return nil
}

func (a *segmentAccumulator) matches(generation string, frontier backupcontract.StreamFrontier) bool {
	if a == nil || a.generation != generation || a.baseSequence != frontier.Sequence ||
		a.fromCursor != frontier.SourceCursor {
		return false
	}
	if !sameSegmentReference(a.baseHead, frontier.Head) {
		return false
	}
	return sameSegmentReference(a.baseCursorHead, frontier.CursorHead)
}

func sameSegmentReference(left, right *backupartifact.SegmentReference) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (a *segmentAccumulator) sortedMessageCursors() []backupartifact.ChannelBoundary {
	cursors := make([]backupartifact.ChannelBoundary, 0, len(a.messageCursor))
	for _, cursor := range a.messageCursor {
		cursors = append(cursors, cursor)
	}
	sort.Slice(cursors, func(i, j int) bool {
		if cursors[i].ChannelType != cursors[j].ChannelType {
			return cursors[i].ChannelType < cursors[j].ChannelType
		}
		return cursors[i].ChannelID < cursors[j].ChannelID
	})
	return cursors
}
