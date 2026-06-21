package conversationactive

import (
	"context"
	"sort"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type activeViewKey struct {
	channelID   string
	channelType int64
}

// ListActiveView returns active conversations from cache and durable store.
func (m *Manager) ListActiveView(ctx context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) (ActiveViewPage, error) {
	if m.store == nil {
		return ActiveViewPage{}, ErrStoreRequired
	}
	if limit <= 0 {
		return ActiveViewPage{Cursor: after, Done: true}, nil
	}

	cacheRows, cacheAfter, cacheAll := m.cacheRowsForActiveView(kind, uid, after)

	dbLimit := limit + len(cacheAll)
	dbRows, _, dbDone, err := m.store.ListConversationActivePage(ctx, kind, uid, after, dbLimit)
	if err != nil {
		return ActiveViewPage{}, err
	}

	merged, err := m.mergeActiveViewRows(ctx, kind, uid, dbRows, cacheRows, cacheAfter, cacheAll)
	if err != nil {
		return ActiveViewPage{}, err
	}
	sortActiveViewRows(merged)

	done := dbDone
	if len(merged) > limit {
		merged = merged[:limit]
		done = false
	}

	return ActiveViewPage{
		Rows:   merged,
		Cursor: activeViewCursor(merged, after),
		Done:   done,
	}, nil
}

func (m *Manager) cacheRowsForActiveView(kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor) ([]metadb.ConversationState, map[activeViewKey]metadb.ConversationState, map[activeViewKey]metadb.ConversationState) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	byChannel := m.cache[uid]
	if len(byChannel) == 0 {
		return nil, nil, nil
	}

	rows := make([]metadb.ConversationState, 0, len(byChannel))
	afterRows := make(map[activeViewKey]metadb.ConversationState, len(byChannel))
	allRows := make(map[activeViewKey]metadb.ConversationState, len(byChannel))
	for key, entry := range byChannel {
		if key.kind != kind {
			continue
		}
		row := activePatchState(entry.patch)
		if row.ActiveAt <= 0 {
			continue
		}
		viewKey := activeViewKey{channelID: key.channelID, channelType: int64(key.channelType)}
		allRows[viewKey] = row
		if !activeRowAfter(row, after) {
			continue
		}
		rows = append(rows, row)
		afterRows[viewKey] = row
	}
	sortActiveViewRows(rows)
	return rows, afterRows, allRows
}

func (m *Manager) mergeActiveViewRows(ctx context.Context, kind metadb.ConversationKind, uid string, dbRows []metadb.ConversationState, cacheRows []metadb.ConversationState, cacheAfter map[activeViewKey]metadb.ConversationState, cacheAll map[activeViewKey]metadb.ConversationState) ([]metadb.ConversationState, error) {
	merged := make([]metadb.ConversationState, 0, len(dbRows)+len(cacheRows))
	mergedKeys := make(map[activeViewKey]struct{}, len(dbRows)+len(cacheRows))
	for _, dbRow := range dbRows {
		key := activeViewKey{channelID: dbRow.ChannelID, channelType: dbRow.ChannelType}
		if cacheRow, ok := cacheAfter[key]; ok {
			merged = append(merged, mergeActiveViewState(dbRow, cacheRow))
			mergedKeys[key] = struct{}{}
			continue
		}
		if _, ok := cacheAll[key]; ok {
			continue
		}
		merged = append(merged, dbRow)
		mergedKeys[key] = struct{}{}
	}
	for _, cacheRow := range cacheRows {
		key := activeViewKey{channelID: cacheRow.ChannelID, channelType: cacheRow.ChannelType}
		if _, ok := mergedKeys[key]; ok {
			continue
		}
		durable, ok, err := m.store.GetConversationState(ctx, kind, uid, cacheRow.ChannelID, cacheRow.ChannelType)
		if err != nil {
			return nil, err
		}
		if ok {
			cacheRow = mergeActiveViewState(durable, cacheRow)
		}
		merged = append(merged, cacheRow)
		mergedKeys[key] = struct{}{}
	}
	return merged, nil
}

func activePatchState(patch ActivePatch) metadb.ConversationState {
	return metadb.ConversationState{
		UID:         patch.UID,
		Kind:        patch.Kind,
		ChannelID:   patch.ChannelID,
		ChannelType: int64(patch.ChannelType),
		ReadSeq:     patch.ReadSeq,
		ActiveAt:    patch.ActiveAtMS,
	}
}

func mergeActiveViewState(dbRow metadb.ConversationState, cacheRow metadb.ConversationState) metadb.ConversationState {
	merged := dbRow
	if cacheRow.ActiveAt > merged.ActiveAt {
		merged.ActiveAt = cacheRow.ActiveAt
	}
	if cacheRow.ReadSeq > merged.ReadSeq {
		merged.ReadSeq = cacheRow.ReadSeq
	}
	if cacheRow.UpdatedAt > merged.UpdatedAt {
		merged.UpdatedAt = cacheRow.UpdatedAt
	}
	return merged
}

func activeRowAfter(row metadb.ConversationState, after metadb.ConversationActiveCursor) bool {
	if after == (metadb.ConversationActiveCursor{}) {
		return true
	}
	if row.ActiveAt != after.ActiveAt {
		return row.ActiveAt < after.ActiveAt
	}
	if row.ChannelID != after.ChannelID {
		return row.ChannelID > after.ChannelID
	}
	return row.ChannelType > after.ChannelType
}

func activeViewCursor(rows []metadb.ConversationState, fallback metadb.ConversationActiveCursor) metadb.ConversationActiveCursor {
	if len(rows) == 0 {
		return fallback
	}
	last := rows[len(rows)-1]
	return metadb.ConversationActiveCursor{ActiveAt: last.ActiveAt, ChannelID: last.ChannelID, ChannelType: last.ChannelType}
}

func sortActiveViewRows(rows []metadb.ConversationState) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ActiveAt != rows[j].ActiveAt {
			return rows[i].ActiveAt > rows[j].ActiveAt
		}
		if rows[i].ChannelID != rows[j].ChannelID {
			return rows[i].ChannelID < rows[j].ChannelID
		}
		return rows[i].ChannelType < rows[j].ChannelType
	})
}
