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

type activeViewCacheRow struct {
	// state is the cache projection exposed through the active view.
	state metadb.ConversationState
	// messageSeq fences this cache projection against a durable delete barrier.
	messageSeq uint64
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

func (m *Manager) cacheRowsForActiveView(kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor) ([]activeViewCacheRow, map[activeViewKey]activeViewCacheRow, map[activeViewKey]activeViewCacheRow) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	byChannel := m.cache[uid]
	if len(byChannel) == 0 {
		return nil, nil, nil
	}

	rows := make([]activeViewCacheRow, 0, len(byChannel))
	afterRows := make(map[activeViewKey]activeViewCacheRow, len(byChannel))
	allRows := make(map[activeViewKey]activeViewCacheRow, len(byChannel))
	for key, entry := range byChannel {
		if key.kind != kind {
			continue
		}
		state := activePatchState(entry.patch)
		if state.ActiveAt <= 0 {
			continue
		}
		row := activeViewCacheRow{state: state, messageSeq: entry.patch.MessageSeq}
		viewKey := activeViewKey{channelID: key.channelID, channelType: int64(key.channelType)}
		allRows[viewKey] = row
		if !activeRowAfter(state, after) {
			continue
		}
		rows = append(rows, row)
		afterRows[viewKey] = row
	}
	sortActiveViewCacheRows(rows)
	return rows, afterRows, allRows
}

func (m *Manager) mergeActiveViewRows(ctx context.Context, kind metadb.ConversationKind, uid string, dbRows []metadb.ConversationState, cacheRows []activeViewCacheRow, cacheAfter map[activeViewKey]activeViewCacheRow, cacheAll map[activeViewKey]activeViewCacheRow) ([]metadb.ConversationState, error) {
	merged := make([]metadb.ConversationState, 0, len(dbRows)+len(cacheRows))
	mergedKeys := make(map[activeViewKey]struct{}, len(dbRows)+len(cacheRows))
	deleteReconciliations := make([]metadb.ConversationDelete, 0)
	for _, dbRow := range dbRows {
		key := activeViewKey{channelID: dbRow.ChannelID, channelType: dbRow.ChannelType}
		if cacheRow, ok := cacheAfter[key]; ok {
			if cacheActivityNeedsDeleteReconciliation(cacheRow.state, cacheRow.messageSeq, dbRow) {
				deleteReconciliations = append(deleteReconciliations, activeViewDeleteReconciliation(dbRow))
			}
			if cacheActivityFencedByDelete(cacheRow.messageSeq, dbRow.DeletedToSeq) {
				merged = append(merged, dbRow)
			} else {
				merged = append(merged, mergeActiveViewState(dbRow, cacheRow.state))
			}
			mergedKeys[key] = struct{}{}
			continue
		}
		if cacheRow, ok := cacheAll[key]; ok {
			if cacheActivityNeedsDeleteReconciliation(cacheRow.state, cacheRow.messageSeq, dbRow) {
				deleteReconciliations = append(deleteReconciliations, activeViewDeleteReconciliation(dbRow))
			}
			if cacheActivityFencedByDelete(cacheRow.messageSeq, dbRow.DeletedToSeq) {
				merged = append(merged, dbRow)
				mergedKeys[key] = struct{}{}
			}
			continue
		}
		merged = append(merged, dbRow)
		mergedKeys[key] = struct{}{}
	}
	for _, cacheRow := range cacheRows {
		key := activeViewKey{channelID: cacheRow.state.ChannelID, channelType: cacheRow.state.ChannelType}
		if _, ok := mergedKeys[key]; ok {
			continue
		}
		durable, ok, err := m.store.GetConversationState(ctx, kind, uid, cacheRow.state.ChannelID, cacheRow.state.ChannelType)
		if err != nil {
			return nil, err
		}
		if ok {
			if cacheActivityNeedsDeleteReconciliation(cacheRow.state, cacheRow.messageSeq, durable) {
				deleteReconciliations = append(deleteReconciliations, activeViewDeleteReconciliation(durable))
			}
			if cacheActivityFencedByDelete(cacheRow.messageSeq, durable.DeletedToSeq) {
				continue
			}
			cacheRow.state = mergeActiveViewState(durable, cacheRow.state)
		}
		merged = append(merged, cacheRow.state)
		mergedKeys[key] = struct{}{}
	}
	m.ApplyConversationDeletes(deleteReconciliations)
	return merged, nil
}

// cacheActivityFencedByDelete reports whether a durable delete barrier rejects
// the message sequence represented by one cached activity observation.
func cacheActivityFencedByDelete(messageSeq, deletedToSeq uint64) bool {
	return deletedToSeq > 0 && (messageSeq == 0 || messageSeq <= deletedToSeq)
}

// cacheActivityNeedsDeleteReconciliation reports whether hydration discovered
// a barrier or durable lag that must be reflected back into the cache indexes.
func cacheActivityNeedsDeleteReconciliation(cacheRow metadb.ConversationState, messageSeq uint64, durable metadb.ConversationState) bool {
	if durable.DeletedToSeq == 0 {
		return false
	}
	if cacheActivityFencedByDelete(messageSeq, durable.DeletedToSeq) {
		return true
	}
	return cacheRow.ActiveAt > durable.ActiveAt || cacheRow.ReadSeq > durable.ReadSeq
}

// activeViewDeleteReconciliation maps hydrated durable state to the exact-row
// cache invalidation contract shared with committed delete callbacks.
func activeViewDeleteReconciliation(durable metadb.ConversationState) metadb.ConversationDelete {
	return metadb.ConversationDelete{
		UID:          durable.UID,
		Kind:         durable.Kind,
		ChannelID:    durable.ChannelID,
		ChannelType:  durable.ChannelType,
		DeletedToSeq: durable.DeletedToSeq,
		UpdatedAt:    durable.UpdatedAt,
	}
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

func sortActiveViewCacheRows(rows []activeViewCacheRow) {
	sort.Slice(rows, func(i, j int) bool {
		left := rows[i].state
		right := rows[j].state
		if left.ActiveAt != right.ActiveAt {
			return left.ActiveAt > right.ActiveAt
		}
		if left.ChannelID != right.ChannelID {
			return left.ChannelID < right.ChannelID
		}
		return left.ChannelType < right.ChannelType
	})
}
