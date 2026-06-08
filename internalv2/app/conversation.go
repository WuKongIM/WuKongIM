package app

import (
	"context"
	"sort"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
)

type conversationPatchProjector interface {
	ProjectActivePatches(context.Context, messageevents.MessageCommitted) ([]conversationusecase.ActivePatch, error)
}

type conversationPatchAuthority interface {
	AdmitPatches(context.Context, []conversationusecase.ActivePatch) error
}

type conversationAuthorityFlusher interface {
	Flush(context.Context) error
}

type conversationAuthorityPendingKey struct {
	// uid owns the pending active patch.
	uid string
	// channelID identifies the pending conversation row.
	channelID string
	// channelType identifies the pending conversation namespace.
	channelType int64
}

func pendingConversationPatchKey(patch conversationusecase.ActivePatch) conversationAuthorityPendingKey {
	return conversationAuthorityPendingKey{uid: patch.UID, channelID: patch.ChannelID, channelType: patch.ChannelType}
}

func sortedPendingConversationPatches(patches map[conversationAuthorityPendingKey]conversationusecase.ActivePatch) []conversationusecase.ActivePatch {
	if len(patches) == 0 {
		return nil
	}
	keys := make([]conversationAuthorityPendingKey, 0, len(patches))
	for key := range patches {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].uid != keys[j].uid {
			return keys[i].uid < keys[j].uid
		}
		if keys[i].channelID != keys[j].channelID {
			return keys[i].channelID < keys[j].channelID
		}
		return keys[i].channelType < keys[j].channelType
	})
	out := make([]conversationusecase.ActivePatch, 0, len(keys))
	for _, key := range keys {
		out = append(out, patches[key])
	}
	return out
}

func conversationActivePatchBatches(patches []conversationusecase.ActivePatch, batchRows int) [][]conversationusecase.ActivePatch {
	if len(patches) == 0 {
		return nil
	}
	if batchRows <= 0 || batchRows >= len(patches) {
		return [][]conversationusecase.ActivePatch{append([]conversationusecase.ActivePatch(nil), patches...)}
	}
	batches := make([][]conversationusecase.ActivePatch, 0, (len(patches)+batchRows-1)/batchRows)
	for start := 0; start < len(patches); start += batchRows {
		end := start + batchRows
		if end > len(patches) {
			end = len(patches)
		}
		batches = append(batches, append([]conversationusecase.ActivePatch(nil), patches[start:end]...))
	}
	return batches
}
