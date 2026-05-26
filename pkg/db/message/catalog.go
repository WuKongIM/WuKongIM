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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if db == nil || db.engine == nil {
		return nil, dberrors.ErrClosed
	}
	prefix := encodeCatalogPrefix()
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	entries := make([]ChannelCatalogEntry, 0)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key, ok := decodeCatalogKey(iter.Key())
		if !ok {
			return nil, fmt.Errorf("%w: corrupt catalog key", dberrors.ErrCorruptValue)
		}
		value, err := iter.Value()
		if err != nil {
			return nil, err
		}
		id, err := decodeCatalogValue(value)
		if err != nil {
			return nil, err
		}
		entries = append(entries, ChannelCatalogEntry{Key: key, ID: id})
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (l *ChannelLog) stageCatalog(batch *engine.Batch) error {
	return batch.Set(encodeCatalogKey(l.key), encodeCatalogValue(l.id))
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
