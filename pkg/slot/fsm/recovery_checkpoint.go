package fsm

import (
	"context"
	"crypto/sha256"
	"encoding/binary"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

var _ multiraft.CheckpointStateMachine = (*stateMachine)(nil)

// recoveryIsolated excludes legacy hash-Slot migration windows whose source
// and destination may share partitions in the same physical metadata database.
func (m *stateMachine) recoveryIsolated() bool {
	m.ownershipMu.RLock()
	defer m.ownershipMu.RUnlock()
	return len(m.incomingDeltaSlots) == 0 && len(m.migrations) == 0
}

// recoveryOwnership identifies the complete current snapshot ownership. A live
// reassignment invalidates an older proof instead of blocking business applies.
func (m *stateMachine) recoveryOwnership() [32]byte {
	m.ownershipMu.RLock()
	slots := m.runtimeSnapshotHashSlotsLocked()
	m.ownershipMu.RUnlock()
	data := make([]byte, 2*len(slots))
	for i, slot := range slots {
		binary.BigEndian.PutUint16(data[i*2:], slot)
	}
	return sha256.Sum256(data)
}

// RecoveryState reads only the small certificate; it never scans business rows.
func (m *stateMachine) RecoveryState(ctx context.Context) (multiraft.CheckpointState, error) {
	if m == nil || m.db == nil || !m.db.MetaDB().RecoveryCheckpointsEnabled() || !m.recoveryIsolated() {
		return multiraft.CheckpointState{}, nil
	}
	_, proof, _, err := m.db.MetaDB().SlotRecoveryCheckpoint(ctx, m.slot)
	return multiraft.CheckpointState{Enabled: true, LiveEpoch: m.db.MetaDB().RecoveryEpoch(), OwnershipDigest: m.recoveryOwnership(), Proof: proof}, err
}

func (m *stateMachine) stageRecoveryCheckpoint(batch *metadb.WriteBatch, cmd multiraft.Command) error {
	proof := cmd.Checkpoint
	if !m.db.MetaDB().RecoveryCheckpointsEnabled() || !m.recoveryIsolated() {
		return batch.SetSlotAppliedIndex(m.slot, cmd.Index)
	}
	if proof != nil && proof.OwnershipDigest != m.recoveryOwnership() {
		return batch.SetSlotAppliedIndex(m.slot, cmd.Index)
	}
	if proof == nil {
		return batch.ClearSlotRecoveryCheckpoint(m.slot, cmd.Index)
	}
	if proof.Version != 1 || proof.AppliedIndex != cmd.Index || proof.AppliedTerm != cmd.Term || uint64(proof.SlotID) != m.slot {
		return metadb.ErrInvalidArgument
	}
	data, err := proof.MarshalBinary()
	if err != nil {
		return err
	}
	return batch.SetSlotRecoveryCheckpointAt(m.slot, cmd.Index, data, proof.LiveEpoch)
}

// PersistRecoveryCheckpoint publishes config/noop boundaries or a new durable
// snapshot anchor. Business entries use the same helper inside their data batch.
func (m *stateMachine) PersistRecoveryCheckpoint(ctx context.Context, proof multiraft.RecoveryCheckpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if proof.AppliedIndex == 0 {
		return nil
	}
	batch := m.db.NewWriteBatch()
	defer batch.Close()
	if err := m.stageRecoveryCheckpoint(batch, multiraft.Command{SlotID: proof.SlotID, Index: proof.AppliedIndex, Term: proof.AppliedTerm, Checkpoint: &proof}); err != nil {
		return err
	}
	return batch.Commit()
}
