package meta

import (
	"context"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// MessageUpdateRetentionCursor bounds background cleanup independently of
// foreground sequence sync. It scans index keys without loading message bodies.
type MessageUpdateRetentionCursor struct {
	ChannelID   string
	ChannelType int64
	MessageSeq  uint64
	MessageID   uint64
}

// ListMessageUpdateRetentionCandidates returns a bounded page of edit identities.
// Eligibility is checked against current retention metadata before any deletion.
func (s *ShardStore) ListMessageUpdateRetentionCandidates(ctx context.Context, after MessageUpdateRetentionCursor, limit int) ([]MessageUpdate, MessageUpdateRetentionCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, after, false, err
	}
	if limit < 1 || limit > 64 {
		return nil, after, false, ErrInvalidArgument
	}
	hs := s.shard.hashSlot
	base := encodeIndexPrefix(hs, TableIDMessageUpdate, 4)
	span := keycodec.NewPrefixSpan(base)
	start := span.Start
	if after.ChannelID != "" {
		pk := KeyParts{String(after.ChannelID), Int64Ordered(after.ChannelType), Uint64(after.MessageID)}
		ik := KeyParts{String(after.ChannelID), Int64Ordered(after.ChannelType), Uint64(after.MessageSeq)}
		key, err := encodeTableIndexKey(hs, TableIDMessageUpdate, 4, ik, pk)
		if err != nil {
			return nil, after, false, err
		}
		start = append(key, 0)
	}
	snap, err := s.shard.db.engine.NewSnapshot()
	if err != nil {
		return nil, after, false, err
	}
	defer snap.Close()
	iter, err := snap.NewIter(engine.Span{Start: start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, after, false, err
	}
	defer iter.Close()
	rows := make([]MessageUpdate, 0, limit)
	cursor := after
	for ok := iter.First(); ok; ok = iter.Next() {
		if err = ctx.Err(); err != nil {
			return nil, after, false, err
		}
		if len(rows) == limit {
			return rows, cursor, false, nil
		}
		ik, pk, valid, e := messageUpdateTable.decodeIndexKey(base, iter.Key(), messageUpdateTable.spec.Indexes[1])
		if e != nil || !valid {
			return nil, after, false, ErrCorruptValue
		}
		row := MessageUpdate{ChannelID: pk[0].S, ChannelType: pk[1].I64, MessageID: pk[2].U64, MessageSeq: ik[2].U64}
		rows = append(rows, row)
		cursor = MessageUpdateRetentionCursor{ChannelID: row.ChannelID, ChannelType: row.ChannelType, MessageSeq: row.MessageSeq, MessageID: row.MessageID}
	}
	return rows, cursor, true, iter.Error()
}

// pruneMessageUpdate removes all state for one retained-away original in the
// same apply batch. The caller has checked the authoritative retention floor.
func pruneMessageUpdate(state *batchCommitState, b *engine.Batch, hs HashSlot, pk KeyParts) error {
	if err := deleteUpdateRow(messageUpdateTable, state, b, hs, pk); err != nil {
		return err
	}
	if err := deleteUpdateRow(messageUpdatePendingTable, state, b, hs, pk); err != nil {
		return err
	}
	prefix, err := encodeKeyParts(encodeRowPrefix(hs, TableIDMessageUpdateRequest), pk)
	if err != nil {
		return err
	}
	span := keycodec.NewPrefixSpan(prefix)
	if err = b.DeleteRange(engine.Span{Start: span.Start, End: span.End}); err != nil {
		return err
	}
	// Retried commands later in this apply batch must not see deleted results.
	for key := range state.tableRows {
		if len(key) >= len(prefix) && key[:len(prefix)] == string(prefix) {
			state.tableRows[key] = tableRowOverlay{exists: false}
		}
	}
	return nil
}
