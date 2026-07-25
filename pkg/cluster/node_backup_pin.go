package cluster

import (
	"context"
	"fmt"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// BackupSourcePinObservation reports one held Slot Raft source interval.
type BackupSourcePinObservation struct {
	// HashSlot and SlotID identify the held local source.
	HashSlot uint16
	SlotID   uint32
	// FirstIndex and LastIndex delimit the currently retained Raft log.
	FirstIndex uint64
	LastIndex  uint64
	// PinnedBytes is a conservative local disk estimate after the durable cursor.
	PinnedBytes uint64
}

// HoldBackupSourcePin acquires an idempotent local compaction hold for the
// current Slot Leader, then measures the retained interval after afterIndex.
func (n *Node) HoldBackupSourcePin(
	ctx context.Context,
	hashSlot uint16,
	afterIndex uint64,
) (BackupSourcePinObservation, error) {
	authority, err := n.ObserveBackupCaptureAuthority(ctx, hashSlot)
	if err != nil {
		return BackupSourcePinObservation{}, err
	}
	if n.defaultSlotRuntime == nil || n.defaultSlotRaftDB == nil || afterIndex == math.MaxUint64 {
		return BackupSourcePinObservation{}, ErrNotStarted
	}
	slotID := multiraft.SlotID(authority.SlotID)
	pinID := backupSourcePinID(hashSlot)
	if err := n.defaultSlotRuntime.SetLogCompactionPin(ctx, slotID, pinID, afterIndex, true); err != nil {
		return BackupSourcePinObservation{}, mapSlotLogRuntimeError(err)
	}
	storage := n.defaultSlotRaftDB.ForSlot(uint64(authority.SlotID))
	first, err := storage.FirstIndex(ctx)
	if err != nil {
		_ = n.defaultSlotRuntime.SetLogCompactionPin(context.Background(), slotID, pinID, 0, false)
		return BackupSourcePinObservation{}, err
	}
	if afterIndex+1 < first {
		_ = n.defaultSlotRuntime.SetLogCompactionPin(context.Background(), slotID, pinID, 0, false)
		return BackupSourcePinObservation{}, ErrBackupSourceCompacted
	}
	last, err := storage.LastIndex(ctx)
	if err != nil {
		_ = n.defaultSlotRuntime.SetLogCompactionPin(context.Background(), slotID, pinID, 0, false)
		return BackupSourcePinObservation{}, err
	}
	var pinnedBytes uint64
	if last >= afterIndex+1 {
		sizer, ok := storage.(multiraft.LogRangeSizer)
		if !ok {
			_ = n.defaultSlotRuntime.SetLogCompactionPin(context.Background(), slotID, pinID, 0, false)
			return BackupSourcePinObservation{}, fmt.Errorf("cluster: Slot Raft storage cannot size backup pin")
		}
		if last == math.MaxUint64 {
			_ = n.defaultSlotRuntime.SetLogCompactionPin(context.Background(), slotID, pinID, 0, false)
			return BackupSourcePinObservation{}, fmt.Errorf("cluster: Slot Raft log index overflow")
		}
		pinnedBytes, err = sizer.LogRangeBytes(ctx, afterIndex+1, last+1)
		if err != nil {
			_ = n.defaultSlotRuntime.SetLogCompactionPin(context.Background(), slotID, pinID, 0, false)
			return BackupSourcePinObservation{}, err
		}
	}
	return BackupSourcePinObservation{
		HashSlot: hashSlot, SlotID: authority.SlotID,
		FirstIndex: first, LastIndex: last, PinnedBytes: pinnedBytes,
	}, nil
}

// ReleaseBackupSourcePin removes this node's idempotent compaction hold from
// the exact physical Slot that acquired it. Leadership and a fresh route are
// deliberately not required so a former leader can release its stale local
// hold after transfer or control-plane remapping.
func (n *Node) ReleaseBackupSourcePin(ctx context.Context, hashSlot uint16, slotID uint32) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil || n.defaultSlotRuntime == nil || slotID == 0 {
		return ErrNotStarted
	}
	return n.defaultSlotRuntime.SetLogCompactionPin(
		ctx, multiraft.SlotID(slotID), backupSourcePinID(hashSlot), 0, false,
	)
}

func backupSourcePinID(hashSlot uint16) string {
	return fmt.Sprintf("backup-hash-slot-%05d", hashSlot)
}
