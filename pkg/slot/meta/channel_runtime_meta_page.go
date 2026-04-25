package meta

import (
	"bytes"
	"context"

	"github.com/cockroachdb/pebble/v2"
)

// ChannelRuntimeMetaCursor identifies the last emitted record in a shard page scan.
type ChannelRuntimeMetaCursor struct {
	ChannelID   string
	ChannelType int64
}

// ListChannelRuntimeMetaPage scans one hash slot page in primary-key order.
func (s *ShardStore) ListChannelRuntimeMetaPage(ctx context.Context, after ChannelRuntimeMetaCursor, limit int) ([]ChannelRuntimeMeta, ChannelRuntimeMetaCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, ChannelRuntimeMetaCursor{}, false, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return nil, ChannelRuntimeMetaCursor{}, false, err
	}
	if err := validateChannelRuntimeMetaCursor(after); err != nil {
		return nil, ChannelRuntimeMetaCursor{}, false, err
	}
	if err := validateChannelRuntimeMetaLimit(limit); err != nil {
		return nil, ChannelRuntimeMetaCursor{}, false, err
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	prefix := encodeChannelRuntimeMetaPrimaryPrefix(s.slot)
	lowerBound := prefix
	if after != (ChannelRuntimeMetaCursor{}) {
		lowerBound = nextPrefix(encodeChannelRuntimeMetaPrimaryKey(s.slot, after.ChannelID, after.ChannelType, channelRuntimeMetaPrimaryFamilyID))
	}

	iter, err := s.db.db.NewIter(&pebble.IterOptions{
		LowerBound: lowerBound,
		UpperBound: nextPrefix(prefix),
	})
	if err != nil {
		return nil, ChannelRuntimeMetaCursor{}, false, err
	}
	defer iter.Close()

	metas := make([]ChannelRuntimeMeta, 0, limit+1)
	for ok := iter.SeekGE(lowerBound); ok; ok = iter.Next() {
		if err := s.db.checkContext(ctx); err != nil {
			return nil, ChannelRuntimeMetaCursor{}, false, err
		}

		key := iter.Key()
		if !bytes.HasPrefix(key, prefix) {
			break
		}
		value, err := iter.ValueAndErr()
		if err != nil {
			return nil, ChannelRuntimeMetaCursor{}, false, err
		}

		meta, familyID, err := decodeChannelRuntimeMetaRecord(key, value)
		if err != nil {
			return nil, ChannelRuntimeMetaCursor{}, false, err
		}
		if familyID != channelRuntimeMetaPrimaryFamilyID {
			continue
		}

		metas = append(metas, meta)
		if len(metas) > limit {
			return metas[:limit], channelRuntimeMetaToCursor(metas[limit-1]), false, nil
		}
	}
	if err := iter.Error(); err != nil {
		return nil, ChannelRuntimeMetaCursor{}, false, err
	}

	cursor := after
	if len(metas) > 0 {
		cursor = channelRuntimeMetaToCursor(metas[len(metas)-1])
	}
	return metas, cursor, true, nil
}

func validateChannelRuntimeMetaCursor(cursor ChannelRuntimeMetaCursor) error {
	if cursor == (ChannelRuntimeMetaCursor{}) {
		return nil
	}
	return validateChannelRuntimeMetaChannelID(cursor.ChannelID)
}

func validateChannelRuntimeMetaLimit(limit int) error {
	if limit <= 0 {
		return ErrInvalidArgument
	}
	return nil
}

func channelRuntimeMetaToCursor(meta ChannelRuntimeMeta) ChannelRuntimeMetaCursor {
	return ChannelRuntimeMetaCursor{
		ChannelID:   meta.ChannelID,
		ChannelType: meta.ChannelType,
	}
}

func encodeChannelRuntimeMetaPrimaryPrefix(hashSlot uint16) []byte {
	return encodeStatePrefix(hashSlot, ChannelRuntimeMetaTable.ID)
}
