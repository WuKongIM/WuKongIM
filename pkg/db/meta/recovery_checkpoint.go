package meta

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"hash/crc32"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

const (
	systemIDRecoverySeal       = 4
	systemIDRecoveryCheckpoint = 5
	systemIDRecoveryDatabase   = 6
	maxRecoveryProofBytes      = 64 << 10
)

// RecoveryCheckpointsEnabled reports whether exclusive startup configured the
// engine seal. Its value is immutable after foreground admission opens.
func (db *MetaDB) RecoveryCheckpointsEnabled() bool { return db != nil && db.recoveryEnabled }

// RecoveryEpoch fences live FSM chains after an uncertified metadata mutation.
func (db *MetaDB) RecoveryEpoch() uint64 { return db.engine.RecoveryEpoch() }

func recoverySystemKey(id uint16) []byte {
	var builder keycodec.Builder
	return bytes.Clone(builder.Reset().Domain(keycodec.DomainMeta).Partition(keycodec.PartitionGlobal, nil).System(0, id).Key())
}

func recoveryCheckpointKey(slotID uint64) []byte {
	return keycodec.AppendUint64(recoverySystemKey(systemIDRecoveryCheckpoint), slotID)
}

// EnableRecoveryCheckpoints is an exclusive startup operation. Offline tools
// need not enable it: unsealed writes are detected on the next product open.
func (db *MetaDB) EnableRecoveryCheckpoints() error {
	if db == nil || db.engine == nil {
		return ErrInvalidArgument
	}
	if db.recoveryEnabled {
		return nil
	}
	start := recoverySystemKey(systemIDRecoveryCheckpoint)
	end := bytes.Clone(start)
	end[len(end)-1]++
	valid, err := db.engine.ConfigureRecoverySeal(recoverySystemKey(systemIDRecoverySeal), engine.Span{Start: start, End: end})
	if err != nil {
		return err
	}
	identity, found, err := db.get(recoverySystemKey(systemIDRecoveryDatabase))
	if err != nil {
		return err
	}
	if found && len(identity) != 16 {
		return ErrCorruptValue
	}
	if !found {
		identity = make([]byte, 16)
		if _, err := rand.Read(identity); err != nil {
			return err
		}
		valid = false
	}
	if !valid {
		// Persist global invalidation before any scoped write can reseal this
		// database. Otherwise dormant Slots could retain an unaware writer's
		// stale proofs and reuse them on a later open with a now-valid seal.
		batch := db.engine.NewBatch()
		defer batch.Close()
		if err := batch.Set(recoverySystemKey(systemIDRecoveryDatabase), identity); err != nil {
			return err
		}
		if err := batch.Commit(true); err != nil {
			return err
		}
	}
	copy(db.recoveryDBID[:], identity)
	db.recoverySealed = valid
	db.recoveryEnabled = true
	return nil
}

// SetSlotRecoveryCheckpoint stores the proof and applied watermark atomically
// with this batch's FSM mutations. The Raft layer owns proof interpretation.
func (b *Batch) SetSlotRecoveryCheckpoint(slotID, index uint64, proof []byte) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	return b.SetSlotRecoveryCheckpointAt(slotID, index, proof, b.db.RecoveryEpoch())
}

// SetSlotRecoveryCheckpointAt requires continuity from the FSM's live anchor.
// A stale epoch retains business writes but removes this Slot's proof at commit.
func (b *Batch) SetSlotRecoveryCheckpointAt(slotID, index uint64, proof []byte, epoch uint64) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if !b.db.recoveryEnabled || len(proof) == 0 || len(proof) > maxRecoveryProofBytes {
		return ErrInvalidArgument
	}
	if err := b.SetSlotAppliedIndex(slotID, index); err != nil {
		return err
	}
	value := make([]byte, 1+16+8+len(proof)+4)
	value[0] = 1
	copy(value[1:17], b.db.recoveryDBID[:])
	binary.BigEndian.PutUint64(value[17:25], index)
	copy(value[25:], proof)
	binary.BigEndian.PutUint32(value[len(value)-4:], crc32.ChecksumIEEE(value[:len(value)-4]))
	key := recoveryCheckpointKey(slotID)
	b.ops = append(b.ops, metaBatchOp{apply: func(_ context.Context, _ *batchCommitState, batch *engine.Batch) error {
		batch.PreserveRecoveryCertificateAt(key, epoch)
		return batch.Set(key, value)
	}})
	b.recoveryScoped = true
	return nil
}

// ClearSlotRecoveryCheckpoint publishes an unproven FSM watermark without
// discarding disjoint Slot proofs. Only ownership-validated FSM writes may use
// this method; arbitrary imports must retain global certificate invalidation.
func (b *Batch) ClearSlotRecoveryCheckpoint(slotID, index uint64) error {
	if err := b.SetSlotAppliedIndex(slotID, index); err != nil {
		return err
	}
	key := recoveryCheckpointKey(slotID)
	b.ops = append(b.ops, metaBatchOp{apply: func(_ context.Context, _ *batchCommitState, batch *engine.Batch) error {
		batch.PreserveRecoveryCertificateAt(key, 0)
		return batch.Delete(key)
	}})
	b.recoveryScoped = true
	return nil
}

// ClearSlotRecoveryCheckpoint forwards a known, currently unproven FSM write.
func (b *WriteBatch) ClearSlotRecoveryCheckpoint(slotID, index uint64) error {
	if err := b.ensure(); err != nil {
		return err
	}
	return b.batch.ClearSlotRecoveryCheckpoint(slotID, index)
}

// SlotRecoveryCheckpoint reads a startup candidate only from an unchanged
// sealed database. Missing/malformed proofs fall back to verified restoration;
// physical read errors remain errors. A pending install can never certify data.
func (db *MetaDB) SlotRecoveryCheckpoint(ctx context.Context, slotID uint64) (uint64, []byte, bool, error) {
	if db == nil || !db.recoveryEnabled || !db.recoverySealed {
		return 0, nil, false, nil
	}
	value, found, err := db.get(recoveryCheckpointKey(slotID))
	if err != nil || !found {
		return 0, nil, false, err
	}
	if len(value) < 30 || len(value) > maxRecoveryProofBytes+29 || value[0] != 1 || !bytes.Equal(value[1:17], db.recoveryDBID[:]) || binary.BigEndian.Uint32(value[len(value)-4:]) != crc32.ChecksumIEEE(value[:len(value)-4]) {
		return 0, nil, false, nil
	}
	index := binary.BigEndian.Uint64(value[17:25])
	applied, err := db.SlotAppliedIndex(ctx, slotID)
	if errors.Is(err, ErrRestoreIncomplete) {
		return 0, nil, false, nil
	}
	if err != nil {
		return 0, nil, false, err
	}
	if index == 0 || applied != index {
		return 0, nil, false, nil
	}
	return index, bytes.Clone(value[25 : len(value)-4]), true, nil
}

// SetSlotRecoveryCheckpoint forwards the same atomic proof through the legacy
// compatibility batch used by the Slot FSM.
func (b *WriteBatch) SetSlotRecoveryCheckpoint(slotID, index uint64, proof []byte) error {
	if err := b.ensure(); err != nil {
		return err
	}
	return b.batch.SetSlotRecoveryCheckpoint(slotID, index, proof)
}

// SetSlotRecoveryCheckpointAt forwards the live anchor's invalidation fence.
func (b *WriteBatch) SetSlotRecoveryCheckpointAt(slotID, index uint64, proof []byte, epoch uint64) error {
	if err := b.ensure(); err != nil {
		return err
	}
	return b.batch.SetSlotRecoveryCheckpointAt(slotID, index, proof, epoch)
}
