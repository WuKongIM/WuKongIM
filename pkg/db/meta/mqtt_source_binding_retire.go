package meta

import (
	"context"
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
)

// Table-26 System 2 stores one closed-lifetime fence per (owner, client):
// bindings of Session generations <= ClosedThrough can never be inserted again.
// System 3 stores one replay marker per Channel owner whose last row retired.
const (
	mqttBindingFenceSystem  = 2
	mqttBindingMarkerSystem = 3
)

var mqttBindingOwnerLayout = KeyLayout{KeyUint8, KeyString, KeyString}

func mqttBindingSystemPrefix(slot HashSlot, system uint16) []byte {
	var b keycodec.Builder
	return b.Reset().Domain(keycodec.DomainMeta).Partition(keycodec.PartitionHashSlot, hashSlotPartitionID(slot)).System(TableIDMQTTSourceBinding, system).Key()
}

func mqttBindingFenceKey(slot HashSlot, k MQTTSourceBindingKey) ([]byte, error) {
	return encodeKeyParts(mqttBindingSystemPrefix(slot, mqttBindingFenceSystem), append(mqttBindingOwnerParts(k.Owner), String(k.Namespace), String(k.ClientID)))
}

func mqttBindingMarkerKey(slot HashSlot, o MQTTBindingOwner) ([]byte, error) {
	return encodeKeyParts(mqttBindingSystemPrefix(slot, mqttBindingMarkerSystem), mqttBindingOwnerParts(o))
}

// loadMQTTBindingFence returns the closed Session generation for one client, or 0.
func loadMQTTBindingFence(state *batchCommitState, slot HashSlot, k MQTTSourceBindingKey) (uint64, error) {
	key, err := mqttBindingFenceKey(slot, k)
	if err != nil {
		return 0, err
	}
	value, found, err := mqttSourceBindingTable.loadBatchValue(state, key)
	if err != nil || !found {
		return 0, err
	}
	env, err := rowcodec.UnwrapBorrowed(key, value)
	if err != nil {
		return 0, err
	}
	if env.Version != 1 || env.Codec != rowcodec.CodecFixed || env.Flags != rowcodec.FlagChecksum || len(env.Payload) != 8 {
		return 0, dberrors.ErrCorruptValue
	}
	closed := binary.BigEndian.Uint64(env.Payload)
	if closed == 0 {
		return 0, dberrors.ErrCorruptValue
	}
	return closed, nil
}

func stageMQTTBindingSystem(state *batchCommitState, b *engine.Batch, key, body []byte) error {
	value := rowcodec.Wrap(key, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, body)
	if err := b.Set(key, value); err != nil {
		return err
	}
	state.tableRows[string(key)] = tableRowOverlay{value: value, exists: true}
	return nil
}

// RetireMQTTSourceBinding deletes one acknowledged Removed tombstone. The caller
// must prove from a fresh Session-Slot read that every lifetime through
// closedThrough has ended; storage only enforces shape and monotonic fencing.
// The fence and, for Channel owners, a replay marker commit with the deletion.
func (b *Batch) RetireMQTTSourceBinding(slot HashSlot, key MQTTSourceBindingKey, expected, closedThrough uint64) (*MQTTSourceBindingResult, error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	if validateMQTTSourceBindingKey(key) != nil || expected == 0 || closedThrough < key.SessionGeneration {
		return nil, dberrors.ErrInvalidArgument
	}
	result := &MQTTSourceBindingResult{}
	b.addOp(slot, func(_ context.Context, state *batchCommitState, batch *engine.Batch) error {
		*result = MQTTSourceBindingResult{Status: MQTTSessionCASConflict}
		pk := mqttSourceBindingPrimaryKey(key)
		old, found, err := loadUpdateRow(mqttSourceBindingTable, state, slot, pk)
		if err != nil || !found {
			return err
		}
		result.CurrentRevision = old.Revision
		if old.Revision != expected || old.Stage != MQTTBindingRemoved || old.ProtectionRevision == 0 && old.Key.Owner.Kind == MQTTBindingChannel {
			return nil
		}
		fence, err := loadMQTTBindingFence(state, slot, key)
		if err != nil {
			return err
		}
		if closedThrough > fence {
			fk, err := mqttBindingFenceKey(slot, key)
			if err != nil {
				return err
			}
			if err = stageMQTTBindingSystem(state, batch, fk, binary.BigEndian.AppendUint64(nil, closedThrough)); err != nil {
				return err
			}
		}
		if key.Owner.Kind == MQTTBindingChannel {
			mk, err := mqttBindingMarkerKey(slot, key.Owner)
			if err != nil {
				return err
			}
			if err = stageMQTTBindingSystem(state, batch, mk, nil); err != nil {
				return err
			}
		}
		if err = deleteUpdateRow(mqttSourceBindingTable, state, batch, slot, pk); err != nil {
			return err
		}
		*result = MQTTSourceBindingResult{Status: MQTTSessionCASApplied}
		return nil
	})
	return result, nil
}

// WriteBatch.RetireMQTTSourceBinding exposes tombstone retirement to the Slot FSM.
func (b *WriteBatch) RetireMQTTSourceBinding(slot uint16, key MQTTSourceBindingKey, expected, closedThrough uint64) (*MQTTSourceBindingResult, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	return b.batch.RetireMQTTSourceBinding(HashSlot(slot), key, expected, closedThrough)
}

// listMQTTReplayMarkers returns up to limit markers strictly after the owner.
func (s *Shard) listMQTTReplayMarkers(ctx context.Context, after MQTTBindingOwner, limit int) ([]MQTTBindingOwner, error) {
	base := mqttBindingSystemPrefix(s.hashSlot, mqttBindingMarkerSystem)
	span := keycodec.NewPrefixSpan(base)
	if after != (MQTTBindingOwner{}) {
		key, err := mqttBindingMarkerKey(s.hashSlot, after)
		if err != nil {
			return nil, err
		}
		span.Start = keycodec.PrefixEnd(key)
	}
	iter, err := s.newTableReadIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var out []MQTTBindingOwner
	for ok := iter.First(); ok && len(out) < limit; ok = iter.Next() {
		if err := contextErr(ctx); err != nil {
			return nil, err
		}
		parts, rest, err := decodeKeyParts(iter.Key()[len(base):], mqttBindingOwnerLayout)
		if err != nil || len(rest) != 0 {
			return nil, dberrors.ErrCorruptValue
		}
		owner := MQTTBindingOwner{Kind: MQTTBindingOwnerKind(parts[0].U8), ID: parts[1].S, Generation: parts[2].S}
		value, err := iter.Value()
		if err != nil {
			return nil, err
		}
		env, err := rowcodec.UnwrapBorrowed(iter.Key(), value)
		if err != nil || env.Version != 1 || env.Codec != rowcodec.CodecFixed || len(env.Payload) != 0 || owner.Kind != MQTTBindingChannel || validateMQTTBindingOwner(owner) != nil {
			return nil, dberrors.ErrCorruptValue
		}
		out = append(out, owner)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return out, nil
}
