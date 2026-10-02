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

// mqttBindingFence is the per-(owner, client) insert fence. Closed fences whole
// ended Session lifetimes; LiveSession/LiveSubscriptionThrough fence ended
// subscriptions of one still-live Session lifetime.
type mqttBindingFence struct {
	Closed, LiveSession, LiveSubscriptionThrough uint64
}

// Blocks reports whether a first insert of key could resurrect a retired row.
func (f mqttBindingFence) Blocks(k MQTTSourceBindingKey) bool {
	return k.SessionGeneration <= f.Closed || k.SessionGeneration == f.LiveSession && k.SubscriptionGeneration <= f.LiveSubscriptionThrough
}

func (f mqttBindingFence) encode() []byte {
	if f.LiveSession == 0 {
		return binary.BigEndian.AppendUint64(nil, f.Closed)
	}
	b := binary.BigEndian.AppendUint64(nil, f.Closed)
	b = binary.BigEndian.AppendUint64(b, f.LiveSession)
	return binary.BigEndian.AppendUint64(b, f.LiveSubscriptionThrough)
}

// loadMQTTBindingFence returns the insert fence for one client, or zero.
func loadMQTTBindingFence(state *batchCommitState, slot HashSlot, k MQTTSourceBindingKey) (mqttBindingFence, error) {
	key, err := mqttBindingFenceKey(slot, k)
	if err != nil {
		return mqttBindingFence{}, err
	}
	value, found, err := mqttSourceBindingTable.loadBatchValue(state, key)
	if err != nil || !found {
		return mqttBindingFence{}, err
	}
	env, err := rowcodec.UnwrapBorrowed(key, value)
	if err != nil {
		return mqttBindingFence{}, err
	}
	if env.Version != 1 || env.Codec != rowcodec.CodecFixed || env.Flags != rowcodec.FlagChecksum {
		return mqttBindingFence{}, dberrors.ErrCorruptValue
	}
	p := env.Payload
	var f mqttBindingFence
	switch len(p) {
	case 8:
		f.Closed = binary.BigEndian.Uint64(p)
		if f.Closed == 0 {
			return mqttBindingFence{}, dberrors.ErrCorruptValue
		}
	case 24:
		f = mqttBindingFence{Closed: binary.BigEndian.Uint64(p), LiveSession: binary.BigEndian.Uint64(p[8:]), LiveSubscriptionThrough: binary.BigEndian.Uint64(p[16:])}
		if f.LiveSession == 0 || f.LiveSubscriptionThrough == 0 || f.LiveSession <= f.Closed {
			return mqttBindingFence{}, dberrors.ErrCorruptValue
		}
	default:
		return mqttBindingFence{}, dberrors.ErrCorruptValue
	}
	return f, nil
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
		if closedThrough > fence.Closed {
			next := mqttBindingFence{Closed: closedThrough}
			// An ended lifetime subsumes any older live watermark.
			if fence.LiveSession > closedThrough {
				next.LiveSession, next.LiveSubscriptionThrough = fence.LiveSession, fence.LiveSubscriptionThrough
			}
			if err = stageMQTTBindingFence(state, batch, slot, key, next); err != nil {
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

// ClearMQTTReplayMarker removes one Channel owner's replay-discovery marker
// after the caller proved replay cleanup has nothing left for that owner. It
// conflicts while any binding row under the owner exists (disk or this batch),
// so a new consumer keeps discovery through its own primary row. Lifetime
// fences are never removed here.
func (b *Batch) ClearMQTTReplayMarker(slot HashSlot, owner MQTTBindingOwner) (*MQTTSourceBindingResult, error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	if owner.Kind != MQTTBindingChannel || validateMQTTBindingOwner(owner) != nil {
		return nil, dberrors.ErrInvalidArgument
	}
	result := &MQTTSourceBindingResult{}
	b.addOp(slot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		*result = MQTTSourceBindingResult{Status: MQTTSessionCASConflict}
		mk, err := mqttBindingMarkerKey(slot, owner)
		if err != nil {
			return err
		}
		if _, found, err := mqttSourceBindingTable.loadBatchValue(state, mk); err != nil || !found {
			return err
		}
		rows, err := mqttBindingOwnerHasRows(ctx, state, slot, owner)
		if err != nil || rows {
			return err
		}
		if err = batch.Delete(mk); err != nil {
			return err
		}
		state.tableRows[string(mk)] = tableRowOverlay{exists: false}
		*result = MQTTSourceBindingResult{Status: MQTTSessionCASApplied}
		return nil
	})
	return result, nil
}

// mqttBindingOwnerHasRows reports whether any primary row under the owner is
// visible to this apply batch. It stops at the first visible row.
func mqttBindingOwnerHasRows(ctx context.Context, state *batchCommitState, slot HashSlot, owner MQTTBindingOwner) (bool, error) {
	prefix, err := encodeKeyParts(encodeRowPrefix(slot, TableIDMQTTSourceBinding), mqttBindingOwnerParts(owner))
	if err != nil {
		return false, err
	}
	span := keycodec.NewPrefixSpan(prefix)
	masked := func(key []byte) bool {
		for _, d := range state.tableDeletes {
			if mqttReclamationContains(d, key) {
				return true
			}
		}
		return false
	}
	for key, row := range state.tableRows {
		if row.exists && mqttReclamationContains(engine.Span{Start: span.Start, End: span.End}, []byte(key)) {
			return true, nil
		}
	}
	iter, err := state.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return false, err
	}
	defer iter.Close()
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := contextErr(ctx); err != nil {
			return false, err
		}
		if row, touched := state.tableRows[string(iter.Key())]; touched && !row.exists {
			continue
		}
		if masked(iter.Key()) {
			continue
		}
		return true, nil
	}
	return false, iter.Error()
}

// WriteBatch.ClearMQTTReplayMarker exposes marker clearing to the Slot FSM.
func (b *WriteBatch) ClearMQTTReplayMarker(slot uint16, owner MQTTBindingOwner) (*MQTTSourceBindingResult, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	return b.batch.ClearMQTTReplayMarker(HashSlot(slot), owner)
}

func stageMQTTBindingFence(state *batchCommitState, b *engine.Batch, slot HashSlot, k MQTTSourceBindingKey, f mqttBindingFence) error {
	key, err := mqttBindingFenceKey(slot, k)
	if err != nil {
		return err
	}
	return stageMQTTBindingSystem(state, b, key, f.encode())
}

// RetireLiveMQTTSourceBinding deletes one acknowledged Removed tombstone of a
// still-live Session lifetime. The caller must prove from a fresh Session-Slot
// read that the Session is still key.SessionGeneration and every subscription
// of it through subscriptionThrough is Removed; new subscriptions allocate
// higher generations, so fencing through that watermark cannot block live work.
func (b *Batch) RetireLiveMQTTSourceBinding(slot HashSlot, key MQTTSourceBindingKey, expected, subscriptionThrough uint64) (*MQTTSourceBindingResult, error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	if validateMQTTSourceBindingKey(key) != nil || expected == 0 || subscriptionThrough < key.SubscriptionGeneration {
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
		// A lower live lifetime cannot move the watermark backwards; it must
		// use ended-lifetime retirement instead.
		if fence.LiveSession > key.SessionGeneration {
			return nil
		}
		if key.SessionGeneration > fence.Closed {
			next := fence
			if next.LiveSession < key.SessionGeneration {
				next.LiveSession, next.LiveSubscriptionThrough = key.SessionGeneration, 0
			}
			if subscriptionThrough > next.LiveSubscriptionThrough {
				next.LiveSubscriptionThrough = subscriptionThrough
			}
			if next != fence {
				if err = stageMQTTBindingFence(state, batch, slot, key, next); err != nil {
					return err
				}
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

// RetireLiveMQTTSourceBinding stages live-Session tombstone retirement.
func (b *WriteBatch) RetireLiveMQTTSourceBinding(slot uint16, key MQTTSourceBindingKey, expected, subscriptionThrough uint64) (*MQTTSourceBindingResult, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	return b.batch.RetireLiveMQTTSourceBinding(HashSlot(slot), key, expected, subscriptionThrough)
}
