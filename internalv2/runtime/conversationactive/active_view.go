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
func (m *Manager) ListActiveView(ctx context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) (ActiveViewPage, error) {
	if limit <= 0 {
		return ActiveViewPage{Cursor: after, Done: true}, nil
	}

	cacheRows, cacheAfter, cacheAll := m.cacheRowsForActiveView(uid, after)

	var dbRows []metadb.UserConversationState
	dbDone := true
	if m.store != nil {
		dbLimit := limit + len(cacheAll)
		rows, _, done, err := m.store.ListUserConversationActivePage(ctx, uid, after, dbLimit)
		if err != nil {
			return ActiveViewPage{}, err
		}
		dbRows = rows
		dbDone = done
	}

	merged := mergeActiveViewRows(dbRows, cacheRows, cacheAfter, cacheAll)
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

func (m *Manager) cacheRowsForActiveView(uid string, after metadb.UserConversationActiveCursor) ([]metadb.UserConversationState, map[activeViewKey]metadb.UserConversationState, map[activeViewKey]metadb.UserConversationState) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	byChannel := m.cache[uid]
	if len(byChannel) == 0 {
		return nil, nil, nil
	}

	rows := make([]metadb.UserConversationState, 0, len(byChannel))
	afterRows := make(map[activeViewKey]metadb.UserConversationState, len(byChannel))
	allRows := make(map[activeViewKey]metadb.UserConversationState, len(byChannel))
	for key, patch := range byChannel {
		row := activePatchState(patch)
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

func mergeActiveViewRows(dbRows []metadb.UserConversationState, cacheRows []metadb.UserConversationState, cacheAfter map[activeViewKey]metadb.UserConversationState, cacheAll map[activeViewKey]metadb.UserConversationState) []metadb.UserConversationState {
	merged := make([]metadb.UserConversationState, 0, len(dbRows)+len(cacheRows))
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
		merged = append(merged, cacheRow)
		mergedKeys[key] = struct{}{}
	}
	return merged
}

func activePatchState(patch ActivePatch) metadb.UserConversationState {
	return metadb.UserConversationState{
		UID:         patch.UID,
		ChannelID:   patch.ChannelID,
		ChannelType: int64(patch.ChannelType),
		ReadSeq:     patch.ReadSeq,
		ActiveAt:    patch.ActiveAtMS,
	}
}

func mergeActiveViewState(dbRow metadb.UserConversationState, cacheRow metadb.UserConversationState) metadb.UserConversationState {
	merged := dbRow
	if cacheRow.ActiveAt > merged.ActiveAt {
		merged.ActiveAt = cacheRow.ActiveAt
		merged.SparseActive = cacheRow.SparseActive
	}
	if cacheRow.ReadSeq > merged.ReadSeq {
		merged.ReadSeq = cacheRow.ReadSeq
	}
	if cacheRow.UpdatedAt > merged.UpdatedAt {
		merged.UpdatedAt = cacheRow.UpdatedAt
	}
	return merged
}

func activeRowAfter(row metadb.UserConversationState, after metadb.UserConversationActiveCursor) bool {
	if after == (metadb.UserConversationActiveCursor{}) {
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

func activeViewCursor(rows []metadb.UserConversationState, fallback metadb.UserConversationActiveCursor) metadb.UserConversationActiveCursor {
	if len(rows) == 0 {
		return fallback
	}
	last := rows[len(rows)-1]
	return metadb.UserConversationActiveCursor{ActiveAt: last.ActiveAt, ChannelID: last.ChannelID, ChannelType: last.ChannelType}
}

func sortActiveViewRows(rows []metadb.UserConversationState) {
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
