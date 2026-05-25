package message

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// LoadCheckpoint loads the durable checkpoint for this channel.
func (l *ChannelLog) LoadCheckpoint(ctx context.Context) (Checkpoint, bool, error) {
	if err := ctx.Err(); err != nil {
		return Checkpoint{}, false, err
	}
	if l == nil || l.db == nil || l.db.engine == nil {
		return Checkpoint{}, false, dberrors.ErrClosed
	}
	value, ok, err := l.db.engine.Get(encodeCheckpointKey(l.key))
	if err != nil || !ok {
		return Checkpoint{}, ok, err
	}
	checkpoint, err := decodeCheckpoint(value)
	if err != nil {
		return Checkpoint{}, false, err
	}
	return checkpoint, true, nil
}

// StoreCheckpoint stores checkpoint without monotonic validation.
func (l *ChannelLog) StoreCheckpoint(ctx context.Context, checkpoint Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l == nil || l.db == nil || l.db.engine == nil {
		return dberrors.ErrClosed
	}
	if err := validateCheckpoint(checkpoint); err != nil {
		return err
	}
	batch := l.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(encodeCheckpointKey(l.key), encodeCheckpoint(checkpoint)); err != nil {
		return err
	}
	if err := l.stageCatalog(batch); err != nil {
		return err
	}
	return batch.Commit(true)
}

// StoreCheckpointMonotonic stores checkpoint after checking durable monotonicity.
func (l *ChannelLog) StoreCheckpointMonotonic(ctx context.Context, checkpoint Checkpoint, visibleHW uint64, leo uint64) error {
	if err := l.validateCheckpointMonotonic(ctx, checkpoint, visibleHW, leo); err != nil {
		return err
	}
	return l.StoreCheckpoint(ctx, checkpoint)
}

func (l *ChannelLog) validateCheckpointMonotonic(ctx context.Context, checkpoint Checkpoint, visibleHW uint64, leo uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateCheckpoint(checkpoint); err != nil {
		return err
	}
	if checkpoint.HW > visibleHW {
		return fmt.Errorf("%w: checkpoint hw %d > visible hw %d", dberrors.ErrCorruptState, checkpoint.HW, visibleHW)
	}
	if checkpoint.HW > leo {
		return fmt.Errorf("%w: checkpoint hw %d > leo %d", dberrors.ErrCorruptState, checkpoint.HW, leo)
	}
	current, ok, err := l.LoadCheckpoint(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if checkpoint.HW < current.HW {
		return fmt.Errorf("%w: checkpoint hw regression %d < %d", dberrors.ErrCorruptState, checkpoint.HW, current.HW)
	}
	if checkpoint.LogStartOffset < current.LogStartOffset {
		return fmt.Errorf("%w: log start regression %d < %d", dberrors.ErrCorruptState, checkpoint.LogStartOffset, current.LogStartOffset)
	}
	if checkpoint.Epoch < current.Epoch {
		return fmt.Errorf("%w: checkpoint epoch regression %d < %d", dberrors.ErrCorruptState, checkpoint.Epoch, current.Epoch)
	}
	return nil
}

func validateCheckpoint(checkpoint Checkpoint) error {
	if checkpoint.LogStartOffset > checkpoint.HW {
		return dberrors.ErrCorruptState
	}
	return nil
}

func encodeCheckpoint(checkpoint Checkpoint) []byte {
	value := make([]byte, 0, 24)
	value = binary.BigEndian.AppendUint64(value, checkpoint.Epoch)
	value = binary.BigEndian.AppendUint64(value, checkpoint.LogStartOffset)
	value = binary.BigEndian.AppendUint64(value, checkpoint.HW)
	return value
}

func decodeCheckpoint(value []byte) (Checkpoint, error) {
	if len(value) != 24 {
		return Checkpoint{}, dberrors.ErrCorruptValue
	}
	return Checkpoint{
		Epoch:          binary.BigEndian.Uint64(value[0:8]),
		LogStartOffset: binary.BigEndian.Uint64(value[8:16]),
		HW:             binary.BigEndian.Uint64(value[16:24]),
	}, nil
}
