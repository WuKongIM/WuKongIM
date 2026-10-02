package meta

import (
	"context"
	"encoding/binary"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
)

// deleteChannelRuntimeMeta retires authority, directory work and inbox admission
// in one batch. Direct deletion reports absence; replicated deletion is idempotent.
func (b *Batch) deleteChannelRuntimeMeta(slot HashSlot, id string, typ int64, requireExisting bool) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validateKeyString(id); err != nil {
		return err
	}
	key := encodeChannelRuntimeMetaRowKey(slot, id, typ, channelRuntimeMetaPrimaryFamilyID)
	b.addOp(slot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		old, found, err := state.loadRuntimeMeta(ctx, slot, key, id, typ)
		if err != nil {
			return err
		}
		if !found && requireExisting {
			return dberrors.ErrNotFound
		}
		if found {
			if err := retireRuntimeAuthority(state, batch, slot, old); err != nil {
				return err
			}
			if err := retireRuntimeDirectory(ctx, state, batch, slot, old); err != nil {
				return err
			}
		}
		if err := invalidateMQTTInboxAdmission(state, batch, slot, id, typ); err != nil {
			return err
		}
		state.runtimeMeta[string(key)] = runtimeMetaOverlay{exists: false}
		return batch.Delete(key)
	})
	return nil
}

// retireRuntimeDirectory preserves business Channel data while withdrawing the
// old projection's readiness and task. Recreation must admit a new generation.
func retireRuntimeDirectory(ctx context.Context, state *batchCommitState, batch *engine.Batch, slot HashSlot, m ChannelRuntimeMeta) error {
	if m.ChannelType != 1 {
		return nil
	}
	taskKey, err := personDirectoryTaskTable.primaryRowKey(slot, personDirectoryTaskPrimaryKey(m.ChannelID, m.ChannelType))
	if err != nil {
		return err
	}
	if err := batch.Delete(taskKey); err != nil {
		return err
	}
	state.tableRows[string(taskKey)] = tableRowOverlay{exists: false}
	channelKey := encodeChannelRowKey(slot, m.ChannelID, m.ChannelType, channelPrimaryFamilyID)
	channel, found, err := state.loadChannel(ctx, channelKey, m.ChannelID, m.ChannelType)
	if err != nil || !found {
		return err
	}
	channel.DirectoryProjectionState = DirectoryProjectionPending
	channel.DirectoryProjectionGeneration = maxUint64(channel.DirectoryProjectionGeneration, m.DirectoryGeneration)
	if err := (&Shard{db: state.db, hashSlot: slot}).stageChannel(batch, channelKey, channel); err != nil {
		return err
	}
	state.channelPublishes[string(channelKey)] = channel
	delete(state.channelDeletes, string(channelKey))
	return nil
}

// Runtime table System 1 permanently retains the authority high water after
// physical deletion. It is not a runtime row and cannot grant read/write access.
func runtimeRetirementKey(slot HashSlot, id string, typ int64) []byte {
	var b keycodec.Builder
	prefix := b.Reset().Domain(keycodec.DomainMeta).Partition(keycodec.PartitionHashSlot, hashSlotPartitionID(slot)).System(TableIDChannelRuntimeMeta, 1).Key()
	key, _ := encodeKeyParts(prefix, channelRuntimeMetaPrimaryKey(id, typ))
	return key
}

func loadRuntimeRetirement(state *batchCommitState, slot HashSlot, id string, typ int64) (uint64, bool, error) {
	key := runtimeRetirementKey(slot, id, typ)
	v, found, err := channelRuntimeMetaTable.loadBatchValue(state, key)
	if err != nil || !found {
		return 0, false, err
	}
	n, err := decodeRuntimeRetirement(key, v)
	return n, err == nil, err
}

// decodeRuntimeRetirement is shared by staged writes and pinned snapshot reads.
// Its checksum binds the authority floor to the exact hash Slot and Channel.
func decodeRuntimeRetirement(key, v []byte) (uint64, error) {
	e, err := rowcodec.UnwrapBorrowed(key, v)
	if err != nil {
		return 0, err
	}
	if e.Version != 1 || e.Codec != rowcodec.CodecFixed || e.Flags != rowcodec.FlagChecksum || len(e.Payload) != 8 {
		return 0, dberrors.ErrCorruptValue
	}
	n := binary.BigEndian.Uint64(e.Payload)
	if n == 0 {
		return 0, dberrors.ErrCorruptValue
	}
	return n, nil
}

// retireRuntimeAuthority runs in the same physical batch as deletion and sees
// preceding creates/deletes in that batch. Repeated deletion never resets it.
func retireRuntimeAuthority(state *batchCommitState, b *engine.Batch, slot HashSlot, m ChannelRuntimeMeta) error {
	old, _, err := loadRuntimeRetirement(state, slot, m.ChannelID, m.ChannelType)
	if err != nil {
		return err
	}
	m = normalizeChannelRuntimeMeta(m)
	n := maxUint64(old, m.ChannelEpoch, m.LeaderEpoch, m.RouteGeneration, m.DirectoryGeneration, m.WriteFenceVersion)
	key := runtimeRetirementKey(slot, m.ChannelID, m.ChannelType)
	v := rowcodec.Wrap(key, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, binary.BigEndian.AppendUint64(nil, n))
	if err = b.Set(key, v); err != nil {
		return err
	}
	if state.tableRows == nil {
		state.tableRows = make(map[string]tableRowOverlay)
	}
	state.tableRows[string(key)] = tableRowOverlay{value: v, exists: true}
	return nil
}

// newRuntimeIncarnation assigns versions above every retired authority field.
// The original create candidate is not a receipt of these authoritative values.
func newRuntimeIncarnation(state *batchCommitState, slot HashSlot, m ChannelRuntimeMeta) (ChannelRuntimeMeta, error) {
	floor, found, err := loadRuntimeRetirement(state, slot, m.ChannelID, m.ChannelType)
	if err != nil {
		return ChannelRuntimeMeta{}, err
	}
	if !found {
		return m, nil
	}
	if floor == math.MaxUint64 {
		return ChannelRuntimeMeta{}, dberrors.ErrConflict
	}
	n := maxUint64(floor+1, m.ChannelEpoch, m.LeaderEpoch, m.RouteGeneration, m.DirectoryGeneration, m.WriteFenceVersion)
	m.ChannelEpoch, m.LeaderEpoch, m.RouteGeneration, m.WriteFenceVersion = n, n, n, n
	if m.ChannelType == 1 {
		m.DirectoryGeneration = n
	}
	return m, nil
}

// rejectRetiredRuntimeUpsert prevents a delayed update from reopening a deleted
// identity. Only the explicit create command can allocate its new incarnation.
func rejectRetiredRuntimeUpsert(state *batchCommitState, slot HashSlot, m ChannelRuntimeMeta) error {
	_, found, err := loadRuntimeRetirement(state, slot, m.ChannelID, m.ChannelType)
	if err != nil {
		return err
	}
	if found {
		return dberrors.ErrConflict
	}
	return nil
}
