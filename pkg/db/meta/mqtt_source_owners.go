package meta

import (
	"cmp"
	"context"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// CompareMQTTBindingOwners follows durable key order: kind, then string byte
// length before contents. Source generations remain distinct across pagination.
func CompareMQTTBindingOwners(a, b MQTTBindingOwner) int {
	if n := cmp.Compare(a.Kind, b.Kind); n != 0 {
		return n
	}
	for _, pair := range [][2]string{{a.ID, b.ID}, {a.Generation, b.Generation}} {
		if n := cmp.Compare(len(pair[0]), len(pair[1])); n != 0 {
			return n
		}
		if n := strings.Compare(pair[0], pair[1]); n != 0 {
			return n
		}
	}
	return 0
}

// ListMQTTSourceOwners discovers one entry per Channel source with a nonremoved
// obligation. Each seek skips all subscribers of that source. A pinned snapshot
// verifies at most limit+1 index/primary witnesses; this is neither consumer
// completion proof nor an integrity audit of skipped subscriber rows.
func (s *Shard) ListMQTTSourceOwners(ctx context.Context, after MQTTBindingOwner, limit int) ([]MQTTBindingOwner, MQTTBindingOwner, bool, error) {
	if limit < 1 || limit > 64 || (after != (MQTTBindingOwner{}) && (after.Kind != MQTTBindingChannel || validateMQTTBindingOwner(after) != nil)) {
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
		return view.ListMQTTSourceOwners(ctx, after, limit)
	}
	const indexID = 4 // Retention includes Preparing, Active and Removing sources.
	table := mqttSourceBindingTable
	index, ok := table.indexByID(indexID)
	if !ok {
		return nil, after, false, dberrors.ErrInvalidArgument
	}
	prefix, err := encodeTableIndexScanPrefix(s.hashSlot, table.spec.ID, indexID, KeyParts{Uint8(uint8(MQTTBindingChannel))})
	if err != nil {
		return nil, after, false, err
	}
	span := keycodec.NewPrefixSpan(prefix)
	if after != (MQTTBindingOwner{}) {
		key, err := encodeTableIndexScanPrefix(s.hashSlot, table.spec.ID, indexID, mqttBindingOwnerParts(after))
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
	rows := make([]MQTTBindingOwner, 0, limit)
	last := after
	for ok := iter.First(); ok; {
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
		if !found || !table.rowMatchesIndex(row, index, parts) || row.Key.Owner.Kind != MQTTBindingChannel || validateMQTTBindingOwner(row.Key.Owner) != nil || (last != (MQTTBindingOwner{}) && CompareMQTTBindingOwners(last, row.Key.Owner) >= 0) {
			return nil, after, false, dberrors.ErrCorruptValue
		}
		if len(rows) == limit {
			return rows, last, false, nil
		}
		last = row.Key.Owner
		rows = append(rows, last)
		key, err := encodeTableIndexScanPrefix(s.hashSlot, table.spec.ID, indexID, mqttBindingOwnerParts(last))
		if err != nil {
			return nil, after, false, err
		}
		ok = iter.SeekGE(keycodec.PrefixEnd(key))
	}
	if err = iter.Error(); err != nil {
		return nil, after, false, err
	}
	if err = contextErr(ctx); err != nil {
		return nil, after, false, err
	}
	return rows, last, true, nil
}
