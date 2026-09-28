package meta

import (
	"cmp"
	"context"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
)

// MQTTSessionCursor is the complete primary identity in durable encoded order.
// It carries no lifetime, revision or execution authority.
type MQTTSessionCursor struct {
	Namespace string `json:"broker_namespace"`
	ClientID  string `json:"client_id"`
}

// CompareMQTTSessionCursors follows key encoding: string length, then bytes.
func CompareMQTTSessionCursors(a, b MQTTSessionCursor) int {
	for _, pair := range [][2]string{{a.Namespace, b.Namespace}, {a.ClientID, b.ClientID}} {
		if n := cmp.Compare(len(pair[0]), len(pair[1])); n != 0 {
			return n
		}
		if n := strings.Compare(pair[0], pair[1]); n != 0 {
			return n
		}
	}
	return 0
}
func validateMQTTSessionCursor(c MQTTSessionCursor) error {
	if validateMQTTIdentity(c.Namespace, 1024) != nil || validateMQTTIdentity(c.ClientID, 1024) != nil {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// MQTTSessionReclamationTarget returns the latest ended lifetime still awaiting
// child reclamation. A positive target is neither quiescence nor remote GC proof.
func MQTTSessionReclamationTarget(s MQTTSession) uint64 {
	if s.Generation == 0 {
		return 0
	}
	through := s.Generation
	if s.State != MQTTSessionEnded {
		through--
	}
	if through <= s.ReclaimedThroughGeneration {
		return 0
	}
	return through
}

// MQTTReclamationIndexResult describes one committed backfill page. Completion
// permits indexed discovery; it does not assert that no cleanup work exists.
type MQTTReclamationIndexResult struct {
	Scanned int
	Done    bool
}

// One table-22 System-1 record per hash Slot certifies coverage of index 3.
// Missing progress must never be interpreted as a completed empty index.
type mqttReclamationIndexProgress struct {
	After MQTTSessionCursor
	Done  bool
}

func mqttReclamationIndexKey(slot HashSlot) []byte {
	var b keycodec.Builder
	return b.Reset().Domain(keycodec.DomainMeta).Partition(keycodec.PartitionHashSlot, hashSlotPartitionID(slot)).System(TableIDMQTTSession, 1).Key()
}
func encodeMQTTReclamationIndexProgress(key []byte, p mqttReclamationIndexProgress) ([]byte, error) {
	if p.After == (MQTTSessionCursor{}) {
		if !p.Done {
			return nil, dberrors.ErrInvalidArgument
		}
	} else if validateMQTTSessionCursor(p.After) != nil {
		return nil, dberrors.ErrInvalidArgument
	}
	body := []byte{0}
	if p.Done {
		body[0] = 1
	}
	body = appendValueString(body, p.After.Namespace)
	body = appendValueString(body, p.After.ClientID)
	return rowcodec.Wrap(key, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, body), nil
}
func decodeMQTTReclamationIndexProgress(key, value []byte) (p mqttReclamationIndexProgress, err error) {
	if len(value) > 2200 {
		return p, dberrors.ErrCorruptValue
	}
	env, err := rowcodec.UnwrapBorrowed(key, value)
	if err != nil {
		return p, err
	}
	body := env.Payload
	if env.Version != 1 || env.Codec != rowcodec.CodecFixed || env.Flags != rowcodec.FlagChecksum || len(body) < 1 || body[0] > 1 {
		return p, dberrors.ErrCorruptValue
	}
	p.Done = body[0] == 1
	body = body[1:]
	p.After.Namespace, body, err = readValueString(body)
	if err != nil {
		return mqttReclamationIndexProgress{}, err
	}
	p.After.ClientID, body, err = readValueString(body)
	if err != nil {
		return mqttReclamationIndexProgress{}, err
	}
	if len(body) != 0 || p.After == (MQTTSessionCursor{}) && !p.Done || p.After != (MQTTSessionCursor{}) && validateMQTTSessionCursor(p.After) != nil {
		return mqttReclamationIndexProgress{}, dberrors.ErrCorruptValue
	}
	return p, nil
}

// BuildMQTTReclamationIndex restores at most 64 Session index entries and its
// primary-key checkpoint in one Slot batch. It never mutates a Session value or
// revision. Every ordinary Session write already maintains the new index, so
// creation or lifecycle changes behind this cursor cannot escape discovery.
func (b *Batch) BuildMQTTReclamationIndex(slot HashSlot) (*MQTTReclamationIndexResult, error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	result := &MQTTReclamationIndexResult{}
	b.addOp(slot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		*result = MQTTReclamationIndexResult{}
		key := mqttReclamationIndexKey(slot)
		value, found, err := mqttSessionTable.loadBatchValue(state, key)
		if err != nil {
			return err
		}
		var progress mqttReclamationIndexProgress
		if found {
			progress, err = decodeMQTTReclamationIndexProgress(key, value)
			if err != nil {
				return err
			}
		}
		if progress.Done {
			result.Done = true
			return nil
		}
		base := encodeRowPrefix(slot, TableIDMQTTSession)
		span := engine.Span{Start: base, End: keycodec.PrefixEnd(base)}
		if progress.After != (MQTTSessionCursor{}) {
			last, err := mqttSessionTable.primaryRowKey(slot, mqttSessionPrimaryKey(progress.After.Namespace, progress.After.ClientID))
			if err != nil {
				return err
			}
			span.Start = keycodec.PrefixEnd(last)
		}
		keys, done, err := mqttReclamationBatchKeys(ctx, state, span)
		if err != nil {
			return err
		}
		for _, key := range keys {
			pk, valid := mqttSessionTable.decodePrimaryRowKey(base, key)
			if !valid {
				return dberrors.ErrCorruptValue
			}
			row, found, err := mqttSessionTable.loadBatchRow(state, slot, pk, key)
			if err != nil {
				return err
			}
			if !found {
				return dberrors.ErrCorruptValue
			}
			// These indexes contain no primary values. Reusing their maintained writer
			// preserves one definition of eligibility without rewriting legacy rows.
			if err := mqttSessionTable.stagePutIndexEntries(batch, slot, row, pk, nil); err != nil {
				return err
			}
			progress.After = MQTTSessionCursor{Namespace: row.Namespace, ClientID: row.ClientID}
		}
		progress.Done = done
		value, err = encodeMQTTReclamationIndexProgress(key, progress)
		if err != nil {
			return err
		}
		if err = batch.Set(key, value); err != nil {
			return err
		}
		state.tableRows[string(key)] = tableRowOverlay{value: value, exists: true}
		*result = MQTTReclamationIndexResult{Scanned: len(keys), Done: done}
		return nil
	})
	return result, nil
}

// ListMQTTSessionReclamation pins coverage and at most limit+1 index/primary
// witnesses. Incomplete coverage returns Conflict instead of false emptiness.
// Every candidate still needs current Session authority and isolation policy.
func (s *Shard) ListMQTTSessionReclamation(ctx context.Context, after MQTTSessionCursor, limit int) ([]MQTTSession, MQTTSessionCursor, bool, error) {
	if limit < 1 || limit > 64 || after != (MQTTSessionCursor{}) && validateMQTTSessionCursor(after) != nil {
		return nil, after, false, dberrors.ErrInvalidArgument
	}
	if err := s.check(ctx); err != nil {
		return nil, after, false, err
	}
	if s.readSnapshot == nil {
		snapshot, err := s.db.engine.NewSnapshot()
		if err != nil {
			return nil, after, false, err
		}
		defer snapshot.Close()
		view := &Shard{db: s.db, hashSlot: s.hashSlot, readSnapshot: snapshot}
		return view.ListMQTTSessionReclamation(ctx, after, limit)
	}
	key := mqttReclamationIndexKey(s.hashSlot)
	value, found, err := s.readSnapshot.Get(key)
	if err != nil {
		return nil, after, false, err
	}
	if !found {
		return nil, after, false, dberrors.ErrConflict
	}
	progress, err := decodeMQTTReclamationIndexProgress(key, value)
	if err != nil {
		return nil, after, false, err
	}
	if !progress.Done {
		return nil, after, false, dberrors.ErrConflict
	}
	const indexID = 3
	index, ok := mqttSessionTable.indexByID(indexID)
	if !ok {
		return nil, after, false, dberrors.ErrInvalidArgument
	}
	base := encodeIndexPrefix(s.hashSlot, TableIDMQTTSession, indexID)
	span := engine.Span{Start: base, End: keycodec.PrefixEnd(base)}
	if after != (MQTTSessionCursor{}) {
		p, err := encodeTableIndexScanPrefix(s.hashSlot, TableIDMQTTSession, indexID, mqttSessionPrimaryKey(after.Namespace, after.ClientID))
		if err != nil {
			return nil, after, false, err
		}
		span.Start = keycodec.PrefixEnd(p)
	}
	iter, err := s.newTableReadIter(span, engine.IterOptions{})
	if err != nil {
		return nil, after, false, err
	}
	defer iter.Close()
	rows := make([]MQTTSession, 0, limit)
	last := after
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := contextErr(ctx); err != nil {
			return nil, after, false, err
		}
		parts, pk, valid, err := mqttSessionTable.decodeIndexKey(base, iter.Key(), index)
		if err != nil {
			return nil, after, false, err
		}
		if !valid {
			return nil, after, false, dberrors.ErrCorruptValue
		}
		row, found, err := snapshotUpdateRow(s.readSnapshot, mqttSessionTable, s.hashSlot, pk)
		if err != nil {
			return nil, after, false, err
		}
		current := MQTTSessionCursor{Namespace: row.Namespace, ClientID: row.ClientID}
		if !found || !mqttSessionTable.rowMatchesIndex(row, index, parts) || MQTTSessionReclamationTarget(row) == 0 || CompareMQTTSessionCursors(last, current) >= 0 {
			return nil, after, false, dberrors.ErrCorruptValue
		}
		if len(rows) == limit {
			return rows, last, false, nil
		}
		rows = append(rows, row)
		last = current
	}
	if err := iter.Error(); err != nil {
		return nil, after, false, err
	}
	if err := contextErr(ctx); err != nil {
		return nil, after, false, err
	}
	return rows, last, true, nil
}

// BuildMQTTReclamationIndex exposes bounded backfill to the Slot FSM.
func (b *WriteBatch) BuildMQTTReclamationIndex(slot uint16) (*MQTTReclamationIndexResult, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	return b.batch.BuildMQTTReclamationIndex(HashSlot(slot))
}
