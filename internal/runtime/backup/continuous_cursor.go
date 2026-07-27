package backup

import (
	"context"
	"fmt"
	"math"
	"sort"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const messageCursorCheckpointInterval uint64 = 1024

const (
	messageCursorLoadWorkingSetMultiplier int64 = 3
	messageCursorMarshalMultiplier        int64 = 3
	messageCursorMarshalOverheadBytes           = 128 << 10
	messageCursorSliceEntryBytes          int64 = 64
)

// messageCursorCheckpoint merges one bounded delta-chain window into a complete
// immutable index, keeping restart key/repository reads O(1024). The returned
// reservation remains owned by the caller until the checkpoint is committed.
func (e *CaptureEngine) messageCursorCheckpoint(ctx context.Context, hashSlot uint16, generation string, current backupcontract.StreamFrontier, delta []backupartifact.ChannelBoundary) ([]backupartifact.ChannelBoundary, int64, error) {
	if e.options.CursorLoader == nil || current.Sequence == 0 || current.CursorHead == nil {
		return nil, 0, fmt.Errorf("%w: cursor checkpoint loader is unavailable", ErrInvalidCapture)
	}
	var reservation int64
	releaseOnError := func(err error) ([]backupartifact.ChannelBoundary, int64, error) {
		if reservation > 0 {
			e.options.MemoryBudget.Release(reservation)
		}
		return nil, 0, err
	}
	for _, boundary := range delta {
		charge := captureCursorHeapOverheadBytes + int64(len(boundary.ChannelID))
		if reservation > math.MaxInt64-charge {
			return nil, 0, fmt.Errorf("%w: cursor checkpoint reservation overflow", ErrInvalidCapture)
		}
		reservation += charge
	}
	if reservation == 0 {
		return nil, 0, fmt.Errorf("%w: cursor checkpoint delta is empty", ErrInvalidCapture)
	}
	if !e.options.MemoryBudget.TryAcquire(reservation) {
		return nil, 0, ErrCaptureMemoryPressure
	}
	latest := make(map[channelCursorIdentity]backupartifact.ChannelBoundary, len(delta))
	for _, boundary := range delta {
		latest[channelCursorIdentity{channelType: boundary.ChannelType, channelID: boundary.ChannelID}] = boundary
	}
	reference := *current.CursorHead
	expectedSequence := current.Sequence
	expectedNextCursor := current.SourceCursor
	expectedHighWatermark := current.SourceHighWatermark
	for segments := uint64(0); segments < messageCursorCheckpointInterval; segments++ {
		loadReservation, err := messageCursorLoadReservation(reference)
		if err != nil {
			return releaseOnError(err)
		}
		if !e.options.MemoryBudget.TryAcquire(loadReservation) {
			return releaseOnError(ErrCaptureMemoryPressure)
		}
		reservation += loadReservation
		body, err := e.options.CursorLoader.Load(ctx, reference)
		if err != nil {
			return releaseOnError(err)
		}
		if int64(len(body)) != reference.PlaintextBytes {
			return releaseOnError(fmt.Errorf("%w: cursor checkpoint size evidence mismatch", ErrInvalidCapture))
		}
		batch, err := backupartifact.LoadMessageCursorBatch(body)
		if err != nil {
			return releaseOnError(err)
		}
		if batch.HashSlot != hashSlot || batch.Generation != generation ||
			batch.Sequence != expectedSequence || batch.NextCursor != expectedNextCursor ||
			batch.SourceHighWatermark > expectedHighWatermark ||
			(segments == 0 && batch.SourceHighWatermark != expectedHighWatermark) {
			return releaseOnError(fmt.Errorf("%w: cursor checkpoint chain is broken", ErrInvalidCapture))
		}
		for _, boundary := range batch.Boundaries {
			identity := channelCursorIdentity{channelType: boundary.ChannelType, channelID: boundary.ChannelID}
			if _, exists := latest[identity]; !exists {
				charge := captureCursorHeapOverheadBytes + int64(len(boundary.ChannelID))
				if !e.options.MemoryBudget.TryAcquire(charge) {
					return releaseOnError(ErrCaptureMemoryPressure)
				}
				reservation += charge
				latest[identity] = boundary
			}
		}
		body = nil
		batch.Boundaries = nil
		e.options.MemoryBudget.Release(loadReservation)
		reservation -= loadReservation
		if batch.Checkpoint || expectedSequence == 1 {
			break
		}
		if batch.Previous == nil {
			return releaseOnError(fmt.Errorf("%w: cursor checkpoint predecessor is missing", ErrInvalidCapture))
		}
		reference = *batch.Previous
		expectedSequence--
		expectedNextCursor = batch.FromCursor
		expectedHighWatermark = batch.SourceHighWatermark
		if segments+1 == messageCursorCheckpointInterval {
			return releaseOnError(fmt.Errorf("%w: cursor checkpoint interval exceeded", ErrInvalidCapture))
		}
	}
	outReservation, err := checkedMessageCursorReservation(int64(len(latest)), messageCursorSliceEntryBytes)
	if err != nil {
		return releaseOnError(err)
	}
	if !e.options.MemoryBudget.TryAcquire(outReservation) {
		return releaseOnError(ErrCaptureMemoryPressure)
	}
	reservation += outReservation
	out := make([]backupartifact.ChannelBoundary, 0, len(latest))
	for _, boundary := range latest {
		out = append(out, boundary)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ChannelType != out[j].ChannelType {
			return out[i].ChannelType < out[j].ChannelType
		}
		return out[i].ChannelID < out[j].ChannelID
	})
	marshalReservation, err := messageCursorMarshalReservation(out)
	if err != nil {
		return releaseOnError(err)
	}
	if !e.options.MemoryBudget.TryAcquire(marshalReservation) {
		return releaseOnError(ErrCaptureMemoryPressure)
	}
	reservation += marshalReservation
	return out, reservation, nil
}

func messageCursorLoadReservation(reference backupartifact.SegmentReference) (int64, error) {
	if reference.PlaintextBytes <= 0 ||
		reference.PlaintextBytes > MaxCaptureSegmentBytes {
		return 0, fmt.Errorf("%w: cursor checkpoint load working set exceeds budget", ErrInvalidCapture)
	}
	return reference.PlaintextBytes * messageCursorLoadWorkingSetMultiplier, nil
}

func messageCursorMarshalReservation(boundaries []backupartifact.ChannelBoundary) (int64, error) {
	encoded := int64(messageCursorMarshalOverheadBytes)
	for _, boundary := range boundaries {
		add := int64(len(boundary.ChannelID)) + 48
		if encoded > math.MaxInt64-add {
			return 0, fmt.Errorf("%w: cursor checkpoint marshal reservation overflow", ErrInvalidCapture)
		}
		encoded += add
	}
	if encoded > math.MaxInt64/messageCursorMarshalMultiplier {
		return 0, fmt.Errorf("%w: cursor checkpoint marshal reservation overflow", ErrInvalidCapture)
	}
	return encoded * messageCursorMarshalMultiplier, nil
}

func checkedMessageCursorReservation(count, perEntry int64) (int64, error) {
	if count < 0 || perEntry <= 0 || count > math.MaxInt64/perEntry {
		return 0, fmt.Errorf("%w: cursor checkpoint reservation overflow", ErrInvalidCapture)
	}
	return count * perEntry, nil
}
