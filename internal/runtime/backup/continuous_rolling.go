package backup

import (
	"context"
	"fmt"
	"math"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// captureStream reconciles one authoritative stream while retaining sparse
// plaintext across reconciliation cycles until the size or time policy seals it.
func (e *CaptureEngine) captureStream(ctx context.Context, hashSlot uint16, lease backupcontract.SlotCaptureLease, stream backupartifact.SegmentStream, current backupcontract.StreamFrontier, target SourceWatermark) (backupcontract.StreamFrontier, error) {
	generation := lease.Generation
	key := captureStreamKey{hashSlot: hashSlot, stream: stream}
	accumulator := e.pendingAccumulator(key, lease, current)
	if accumulator != nil && accumulator.hasRecords() &&
		e.options.Clock.Now().Sub(accumulator.openedAt) >= e.options.Policy.MaxOpenDuration {
		var err error
		current, err = e.commitAccumulator(ctx, hashSlot, generation, stream, current, accumulator)
		if err != nil {
			return backupcontract.StreamFrontier{}, err
		}
		e.removePendingAccumulator(key, accumulator)
		accumulator = nil
	}
	if accumulator != nil && accumulator.scanDone {
		if accumulator.target.Position >= target.Position {
			return current, nil
		}
		accumulator.scanDone = false
		accumulator.scanTarget = target
	}
	if accumulator == nil && target.Position <= current.SourceHighWatermark {
		return current, nil
	}
	if accumulator == nil {
		accumulator = newSegmentAccumulator(lease, current, e.options.Clock.Now())
		e.storePendingAccumulator(key, accumulator)
	}
	scanTarget := target
	if stream == backupartifact.SegmentStreamMessages &&
		accumulator.scanTarget.Position > 0 && !accumulator.scanDone {
		scanTarget = accumulator.scanTarget
	}
	cursor := accumulator.nextCursor
	for pageCount := 0; ; pageCount++ {
		previousPosition := current.SourceHighWatermark
		if accumulator.target.Position > previousPosition {
			previousPosition = accumulator.target.Position
		}
		requestBytes := e.options.Policy.TargetSegmentBytes
		reservation, err := captureReadReservation(
			stream,
			requestBytes,
			e.options.Policy.MaxSegmentBytes,
			e.options.Policy.PageRecords,
		)
		if err != nil {
			return backupcontract.StreamFrontier{}, err
		}
		if !e.options.MemoryBudget.TryAcquire(reservation) {
			return backupcontract.StreamFrontier{}, ErrCaptureMemoryPressure
		}
		page, readErr := e.options.Source.ReadPage(ctx, SourcePageRequest{
			HashSlot: hashSlot, Stream: stream, Generation: generation,
			CursorSequence:     current.Sequence,
			CursorSourceCursor: current.SourceCursor,
			CursorHead:         streamCursorHead(stream, current), AfterCursor: cursor,
			BaselineCursorHead: streamBaselineCursorHead(stream, current),
			ThroughPosition:    scanTarget.Position, ThroughCursor: scanTarget.CutCursor,
			MaxBytes:       requestBytes,
			MaxRecordBytes: e.options.Policy.MaxSegmentBytes,
			MaxRecords:     e.options.Policy.PageRecords,
		})
		if readErr != nil {
			e.options.MemoryBudget.Release(reservation)
			return backupcontract.StreamFrontier{}, readErr
		}
		accounting, validateErr := validateSourcePage(
			stream, cursor, previousPosition, scanTarget.Position, page, e.options.Policy,
		)
		if validateErr != nil || !validCapturePageSize(page, accounting.encodedBytes, requestBytes) {
			e.options.MemoryBudget.Release(reservation)
			if validateErr != nil {
				return backupcontract.StreamFrontier{}, validateErr
			}
			return backupcontract.StreamFrontier{}, fmt.Errorf("%w: source page exceeds rolling target without one oversized record", ErrInvalidCapture)
		}
		retainedReservation, err := capturePageReservation(accounting)
		if err != nil {
			e.options.MemoryBudget.Release(reservation)
			return backupcontract.StreamFrontier{}, err
		}
		if retainedReservation > reservation {
			if !e.options.MemoryBudget.TryAcquire(retainedReservation - reservation) {
				e.options.MemoryBudget.Release(reservation)
				return backupcontract.StreamFrontier{}, ErrCaptureMemoryPressure
			}
		} else {
			e.options.MemoryBudget.Release(reservation - retainedReservation)
		}

		if accumulator.hasRecords() &&
			(accumulator.bytes+accounting.encodedBytes > e.options.Policy.TargetSegmentBytes ||
				accumulator.bytes >= e.options.Policy.TargetSegmentBytes ||
				e.options.Clock.Now().Sub(accumulator.openedAt) >= e.options.Policy.MaxOpenDuration) {
			current, err = e.commitAccumulator(ctx, hashSlot, generation, stream, current, accumulator)
			if err != nil {
				e.options.MemoryBudget.Release(retainedReservation)
				return backupcontract.StreamFrontier{}, err
			}
			e.removePendingAccumulator(key, accumulator)
			accumulator = newSegmentAccumulator(lease, current, e.options.Clock.Now())
			e.storePendingAccumulator(key, accumulator)
		}
		if err := accumulator.append(page, scanTarget, retainedReservation); err != nil {
			e.options.MemoryBudget.Release(retainedReservation)
			return backupcontract.StreamFrontier{}, err
		}
		if page.Done {
			if acknowledger, ok := e.options.Source.(SourcePageAcknowledger); ok {
				acknowledger.AcknowledgeSourcePage(hashSlot, stream, scanTarget.CutCursor)
			}
		}
		cursor = page.NextCursor
		if accumulator.hasRecords() &&
			(accumulator.bytes >= e.options.Policy.TargetSegmentBytes ||
				e.options.Clock.Now().Sub(accumulator.openedAt) >= e.options.Policy.MaxOpenDuration) {
			current, err = e.commitAccumulator(ctx, hashSlot, generation, stream, current, accumulator)
			if err != nil {
				return backupcontract.StreamFrontier{}, err
			}
			e.removePendingAccumulator(key, accumulator)
			if page.Done {
				return current, nil
			}
			accumulator = newSegmentAccumulator(lease, current, e.options.Clock.Now())
			e.storePendingAccumulator(key, accumulator)
		}
		if page.Done {
			if !accumulator.hasRecords() {
				e.removePendingAccumulator(key, accumulator)
				current.SourceCursor = cursor
				current.SourceHighWatermark = accumulator.target.Position
				current.WatermarkAtUnixMillis = accumulator.target.CommittedAtUnixMillis
			}
			return current, nil
		}
		if pageCount+1 >= e.options.Policy.PagesPerReconcile {
			// Wake is a lossy hint; the periodic poll remains the correctness
			// path. Under Run, the coalescing scheduler moves this Slot to the
			// queue tail so one hot Slot cannot monopolize a worker.
			e.Wake(hashSlot)
			return current, nil
		}
	}
}

// commitAccumulator encodes and dual-commits one non-empty bounded segment.
// The caller removes the accumulator only after this method succeeds.
func (e *CaptureEngine) commitAccumulator(ctx context.Context, hashSlot uint16, generation string, stream backupartifact.SegmentStream, current backupcontract.StreamFrontier, accumulator *segmentAccumulator) (backupcontract.StreamFrontier, error) {
	if current.Sequence == math.MaxUint64 {
		return backupcontract.StreamFrontier{}, fmt.Errorf("%w: segment sequence overflow", ErrInvalidCapture)
	}
	sequence := current.Sequence + 1
	watermarkAtUnixMillis := accumulator.target.CommittedAtUnixMillis
	if stream == backupartifact.SegmentStreamMessages {
		watermarkAtUnixMillis = olderPositiveTime(
			current.WatermarkAtUnixMillis,
			watermarkAtUnixMillis,
		)
	}
	batch := backupartifact.SegmentBatch{
		HashSlot: hashSlot, Stream: stream, Generation: generation, Sequence: sequence,
		Previous: current.Head, FromCursor: accumulator.fromCursor, NextCursor: accumulator.nextCursor,
		SourceHighWatermark: accumulator.target.Position, WatermarkAtUnixMillis: watermarkAtUnixMillis,
		Records: accumulator.records, MessageCursors: accumulator.sortedMessageCursors(),
	}
	body, err := backupartifact.MarshalSegmentBatch(batch)
	if err != nil {
		return backupcontract.StreamFrontier{}, err
	}
	if int64(len(body)) > e.options.Policy.MaxSegmentBytes {
		return backupcontract.StreamFrontier{}, fmt.Errorf("%w: encoded segment exceeds rolling hard limit", ErrInvalidCapture)
	}
	reference, err := e.options.Segments.Commit(ctx, backupartifact.SegmentDescriptor{
		Logical: backupartifact.SegmentLogicalDescriptor{
			RepositoryID: e.options.RepositoryID, SourceClusterID: e.options.SourceClusterID,
			SourceGeneration: e.options.SourceGeneration, Generation: generation,
			HashSlot: hashSlot, Stream: stream, Sequence: sequence, RecordCount: uint64(len(accumulator.records)),
		},
		Previous:              cloneRuntimeSegmentReference(batch.Previous),
		SourceHighWatermark:   batch.SourceHighWatermark,
		WatermarkAtUnixMillis: batch.WatermarkAtUnixMillis,
		KMSKeyID:              e.options.KMSKeyID,
	}, body)
	if err != nil {
		return backupcontract.StreamFrontier{}, err
	}
	if err := validateCommittedSegmentReference(reference); err != nil {
		return backupcontract.StreamFrontier{}, err
	}
	committedPlaintextBytes := uint64(reference.PlaintextBytes)
	var cursorReference *backupartifact.SegmentReference
	if stream == backupartifact.SegmentStreamMessages {
		cursorBoundaries := accumulator.sortedMessageCursors()
		cursorPrevious := current.CursorHead
		checkpoint := sequence%messageCursorCheckpointInterval == 0
		var cursorWorkingSetReservation int64
		if checkpoint {
			cursorBoundaries, cursorWorkingSetReservation, err = e.messageCursorCheckpoint(ctx, hashSlot, generation, current, cursorBoundaries)
			if err != nil {
				return backupcontract.StreamFrontier{}, err
			}
			cursorPrevious = nil
		} else {
			cursorWorkingSetReservation, err = messageCursorMarshalReservation(cursorBoundaries)
			if err != nil {
				return backupcontract.StreamFrontier{}, err
			}
			if !e.options.MemoryBudget.TryAcquire(cursorWorkingSetReservation) {
				return backupcontract.StreamFrontier{}, ErrCaptureMemoryPressure
			}
		}
		if cursorWorkingSetReservation > 0 {
			defer e.options.MemoryBudget.Release(cursorWorkingSetReservation)
		}
		cursorBody, err := backupartifact.MarshalMessageCursorBatch(backupartifact.MessageCursorBatch{
			HashSlot: hashSlot, Generation: generation, Sequence: sequence,
			Checkpoint: checkpoint, Previous: cursorPrevious,
			FromCursor: accumulator.fromCursor, NextCursor: accumulator.nextCursor,
			SourceHighWatermark:   accumulator.target.Position,
			WatermarkAtUnixMillis: watermarkAtUnixMillis,
			Boundaries:            cursorBoundaries,
		})
		if err != nil {
			return backupcontract.StreamFrontier{}, err
		}
		committedCursor, err := e.options.Segments.Commit(ctx, backupartifact.SegmentDescriptor{
			Logical: backupartifact.SegmentLogicalDescriptor{
				RepositoryID: e.options.RepositoryID, SourceClusterID: e.options.SourceClusterID,
				SourceGeneration: e.options.SourceGeneration, Generation: generation,
				HashSlot: hashSlot, Stream: backupartifact.SegmentStreamMessageCursor,
				Sequence: sequence, RecordCount: uint64(len(cursorBoundaries)),
			},
			Previous:              cloneRuntimeSegmentReference(cursorPrevious),
			Checkpoint:            checkpoint,
			SourceHighWatermark:   accumulator.target.Position,
			WatermarkAtUnixMillis: watermarkAtUnixMillis,
			KMSKeyID:              e.options.KMSKeyID,
		}, cursorBody)
		if err != nil {
			return backupcontract.StreamFrontier{}, err
		}
		if err := validateCommittedSegmentReference(committedCursor); err != nil {
			return backupcontract.StreamFrontier{}, err
		}
		cursorReference = &committedCursor
		if uint64(committedCursor.PlaintextBytes) > math.MaxUint64-committedPlaintextBytes {
			return backupcontract.StreamFrontier{}, fmt.Errorf("%w: generation byte accounting overflow", ErrInvalidCapture)
		}
		committedPlaintextBytes += uint64(committedCursor.PlaintextBytes)
	}
	if current.CapturedPlaintextBytes > math.MaxUint64-committedPlaintextBytes {
		return backupcontract.StreamFrontier{}, fmt.Errorf("%w: generation byte accounting overflow", ErrInvalidCapture)
	}
	current.Sequence = sequence
	current.Head = &reference
	if cursorReference != nil {
		current.CursorHead = cursorReference
	}
	current.SourceCursor = accumulator.nextCursor
	current.SourceHighWatermark = accumulator.target.Position
	current.WatermarkAtUnixMillis = watermarkAtUnixMillis
	current.CapturedPlaintextBytes += committedPlaintextBytes
	return current, nil
}

func streamCursorHead(stream backupartifact.SegmentStream, current backupcontract.StreamFrontier) *backupartifact.SegmentReference {
	if stream != backupartifact.SegmentStreamMessages {
		return nil
	}
	return cloneRuntimeSegmentReference(current.CursorHead)
}

func streamBaselineCursorHead(stream backupartifact.SegmentStream, current backupcontract.StreamFrontier) *backupartifact.SegmentReference {
	if stream != backupartifact.SegmentStreamMessages {
		return nil
	}
	return cloneRuntimeSegmentReference(current.BaselineCursorHead)
}

func capturePageReservation(accounting sourcePageAccounting) (int64, error) {
	if accounting.encodedBytes < 0 || accounting.memoryBytes < 0 ||
		accounting.encodedBytes > (math.MaxInt64-accounting.memoryBytes)/2 {
		return 0, fmt.Errorf("%w: capture page reservation overflow", ErrInvalidCapture)
	}
	return accounting.memoryBytes + 2*accounting.encodedBytes, nil
}

func captureReadReservation(stream backupartifact.SegmentStream, targetBytes, maxRecordBytes int64, maxRecords int) (int64, error) {
	if targetBytes <= 0 || maxRecordBytes <= 0 || maxRecords <= 0 {
		return 0, fmt.Errorf("%w: capture read reservation is invalid", ErrInvalidCapture)
	}
	perRecord := captureRecordHeapOverheadBytes
	if stream == backupartifact.SegmentStreamMessages {
		perRecord += captureCursorHeapOverheadBytes
	}
	if int64(maxRecords) > (math.MaxInt64-targetBytes)/perRecord {
		return 0, fmt.Errorf("%w: capture read reservation overflow", ErrInvalidCapture)
	}
	targetReservation := targetBytes + int64(maxRecords)*perRecord
	oversizedReservation := maxRecordBytes + captureRecordHeapOverheadBytes
	if stream == backupartifact.SegmentStreamMessages {
		oversizedReservation += captureCursorHeapOverheadBytes
	}
	if oversizedReservation > targetReservation {
		return oversizedReservation, nil
	}
	return targetReservation, nil
}

func validCapturePageSize(page SourcePage, pageBytes, targetBytes int64) bool {
	if pageBytes <= targetBytes {
		return true
	}
	return len(page.Records) == 1
}
