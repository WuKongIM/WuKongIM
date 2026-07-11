package message

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// ListChannels returns all channels known to the message domain.
func (db *MessageDB) ListChannels(ctx context.Context) ([]ChannelCatalogEntry, error) {
	if err := db.beginUse(); err != nil {
		return nil, err
	}
	defer db.endUse()
	entries, _, _, err := db.listChannelsPage(ctx, "", 0)
	return entries, err
}

// ListChannelsPage returns one ordered catalog page after the exclusive cursor.
func (db *MessageDB) ListChannelsPage(ctx context.Context, after ChannelKey, limit int) ([]ChannelCatalogEntry, ChannelKey, bool, error) {
	if err := db.beginUse(); err != nil {
		return nil, "", false, err
	}
	defer db.endUse()
	return db.listChannelsPage(ctx, after, limit)
}

func (db *MessageDB) listChannelsPage(ctx context.Context, after ChannelKey, limit int) ([]ChannelCatalogEntry, ChannelKey, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", false, err
	}
	prefix := encodeCatalogPrefix()
	span := keycodec.NewPrefixSpan(prefix)
	start := span.Start
	if after != "" {
		start = encodeCatalogKey(after)
	}
	iter, err := db.engine.NewIter(engine.Span{Start: start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, "", false, err
	}
	defer iter.Close()

	entries := make([]ChannelCatalogEntry, 0)
	if limit > 0 {
		entries = make([]ChannelCatalogEntry, 0, limit)
	}
	var last ChannelKey
	more := false
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, "", false, err
		}
		key, ok := decodeCatalogKey(iter.Key())
		if !ok {
			return nil, "", false, fmt.Errorf("%w: corrupt catalog key", dberrors.ErrCorruptValue)
		}
		if after != "" && key <= after {
			continue
		}
		value, err := iter.Value()
		if err != nil {
			return nil, "", false, err
		}
		id, err := decodeCatalogValue(value)
		if err != nil {
			return nil, "", false, err
		}
		if limit > 0 && len(entries) == limit {
			more = true
			break
		}
		entries = append(entries, ChannelCatalogEntry{Key: key, ID: id})
		last = key
	}
	if err := iter.Error(); err != nil {
		return nil, "", false, err
	}
	if len(entries) == 0 {
		last = ""
	}
	return entries, last, more, nil
}

func (l *channelEntry) stageCatalog(batch *engine.Batch) error {
	cache := l.appendKeyCache
	return batch.Set(cache.catalogKey, cache.catalogValue)
}

func encodeCatalogValue(id ChannelID) []byte {
	value := keycodec.AppendString(nil, id.ID)
	return append(value, id.Type)
}

func decodeCatalogValue(value []byte) (ChannelID, error) {
	id, rest, err := keycodec.ReadString(value)
	if err != nil {
		return ChannelID{}, dberrors.ErrCorruptValue
	}
	if len(rest) != 1 {
		return ChannelID{}, dberrors.ErrCorruptValue
	}
	return ChannelID{ID: id, Type: rest[0]}, nil
}
