package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// ListMQTTReplaySources retains cleanup discovery after the last consumer has
// left. Primary tombstones remain scheduling hints, never retention permission.
// One pinned row per owner prefix bounds reads independently of subscriber count.
func (s *Shard) ListMQTTReplaySources(ctx context.Context, after MQTTBindingOwner, limit int) ([]MQTTBindingOwner, MQTTBindingOwner, bool, error) {
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
		return view.ListMQTTReplaySources(ctx, after, limit)
	}
	table := mqttSourceBindingTable
	base := encodeRowPrefix(s.hashSlot, table.spec.ID)
	prefix := KeyParts{Uint8(uint8(MQTTBindingChannel))}
	encoded, err := encodeKeyParts(base, prefix)
	if err != nil {
		return nil, after, false, err
	}
	span := keycodec.NewPrefixSpan(encoded)
	if after != (MQTTBindingOwner{}) {
		key, err := encodeKeyParts(base, mqttBindingOwnerParts(after))
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
	rows := make([]MQTTBindingOwner, 0, limit)
	last := after
	for ok := iter.First(); ok; {
		if err := contextErr(ctx); err != nil {
			return nil, after, false, err
		}
		pk, valid := table.decodePrimaryRowKey(base, iter.Key())
		if !valid || !keyPartsHasPrefix(pk, prefix) {
			return nil, after, false, dberrors.ErrCorruptValue
		}
		value, err := iter.Value()
		if err != nil {
			return nil, after, false, err
		}
		row, err := table.decodeValue(iter.Key(), pk, value)
		if err != nil {
			return nil, after, false, err
		}
		owner := row.Key.Owner
		if owner.Kind != MQTTBindingChannel || validateMQTTBindingOwner(owner) != nil || (last != (MQTTBindingOwner{}) && CompareMQTTBindingOwners(last, owner) >= 0) {
			return nil, after, false, dberrors.ErrCorruptValue
		}
		if len(rows) == limit+1 {
			break
		}
		last = owner
		rows = append(rows, owner)
		key, err := encodeKeyParts(base, mqttBindingOwnerParts(owner))
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
	// Retired owners keep cleanup discoverable through System-3 markers.
	markers, err := s.listMQTTReplayMarkers(ctx, after, limit+1)
	if err != nil {
		return nil, after, false, err
	}
	merged := make([]MQTTBindingOwner, 0, len(rows)+len(markers))
	for i, j := 0, 0; i < len(rows) || j < len(markers); {
		switch {
		case j == len(markers) || i < len(rows) && CompareMQTTBindingOwners(rows[i], markers[j]) < 0:
			merged = append(merged, rows[i])
			i++
		case i == len(rows) || CompareMQTTBindingOwners(rows[i], markers[j]) > 0:
			merged = append(merged, markers[j])
			j++
		default:
			merged = append(merged, rows[i])
			i, j = i+1, j+1
		}
	}
	if len(merged) > limit {
		return merged[:limit], merged[limit-1], false, nil
	}
	if len(merged) > 0 {
		last = merged[len(merged)-1]
	}
	return merged, last, true, nil
}
