package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// readMQTTSourceRetention checks at most limit+1 witnesses in one snapshot.
// Unlike generic index scans, a damaged witness cannot disappear from a floor.
func (s *Shard) readMQTTSourceRetention(ctx context.Context, owner MQTTBindingOwner, after MQTTSourceBindingRetentionCursor, limit int) ([]MQTTSourceBinding, MQTTSourceBindingRetentionCursor, bool, error) {
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
		return view.readMQTTSourceRetention(ctx, owner, after, limit)
	}
	const indexID = 4
	table := mqttSourceBindingTable
	index, ok := table.indexByID(indexID)
	if !ok {
		return nil, after, false, dberrors.ErrInvalidArgument
	}
	prefix, err := encodeTableIndexScanPrefix(s.hashSlot, table.spec.ID, indexID, mqttBindingOwnerParts(owner))
	if err != nil {
		return nil, after, false, err
	}
	span := keycodec.NewPrefixSpan(prefix)
	if after != (MQTTSourceBindingRetentionCursor{}) {
		key, err := encodeTableIndexScanPrefix(s.hashSlot, table.spec.ID, indexID, mqttSourceBindingRetentionParts(after.Key, after.CompletedThrough))
		if err != nil {
			return nil, after, false, err
		}
		span.Start = keycodec.PrefixEnd(key)
	}
	iter, err := s.newTableReadIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, after, false, err
	}
	defer iter.Close()
	base := encodeIndexPrefix(s.hashSlot, table.spec.ID, indexID)
	rows := make([]MQTTSourceBinding, 0, limit)
	last := after
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := contextErr(ctx); err != nil {
			return nil, after, false, err
		}
		parts, primary, valid, err := table.decodeIndexKey(base, iter.Key(), index)
		if err != nil {
			return nil, after, false, err
		}
		if !valid {
			return nil, after, false, dberrors.ErrCorruptValue
		}
		row, found, err := snapshotUpdateRow(s.readSnapshot, table, s.hashSlot, primary)
		if err != nil {
			return nil, after, false, err
		}
		if !found || row.Key.Owner != owner || ValidateMQTTSourceBinding(row) != nil || !table.rowMatchesIndex(row, index, parts) {
			return nil, after, false, dberrors.ErrCorruptValue
		}
		if len(rows) == limit {
			return rows, last, false, nil
		}
		rows = append(rows, row)
		last = MQTTSourceBindingRetentionCursor{Key: row.Key, CompletedThrough: row.CompletedThrough}
	}
	if err = iter.Error(); err != nil {
		return nil, after, false, err
	}
	if err = contextErr(ctx); err != nil {
		return nil, after, false, err
	}
	// Preserve the existing terminal-page cursor convention.
	return rows, after, true, nil
}
