package app

import (
	"sort"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func conversationRowAfter(row metadb.ConversationState, after metadb.ConversationActiveCursor) bool {
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

func conversationRowsCursor(rows []metadb.ConversationState, fallback metadb.ConversationActiveCursor) metadb.ConversationActiveCursor {
	if len(rows) == 0 {
		return fallback
	}
	last := rows[len(rows)-1]
	return metadb.ConversationActiveCursor{ActiveAt: last.ActiveAt, ChannelID: last.ChannelID, ChannelType: last.ChannelType}
}

func sortConversationRows(rows []metadb.ConversationState) {
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

func mergeConversationState(existing, next metadb.ConversationState) metadb.ConversationState {
	merged := existing
	if next.ActiveAt > merged.ActiveAt {
		merged.ActiveAt = next.ActiveAt
		merged.SparseActive = next.SparseActive
	} else if next.ActiveAt == merged.ActiveAt && next.UpdatedAt > merged.UpdatedAt {
		merged.SparseActive = next.SparseActive
	}
	if next.UpdatedAt > merged.UpdatedAt {
		merged.UpdatedAt = next.UpdatedAt
	}
	if next.ReadSeq > merged.ReadSeq {
		merged.ReadSeq = next.ReadSeq
	}
	if next.DeletedToSeq > merged.DeletedToSeq {
		merged.DeletedToSeq = next.DeletedToSeq
	}
	return merged
}
