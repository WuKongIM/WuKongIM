package app

import (
	"context"
	"sort"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
)

type committedSinkGroup []message.CommittedSink

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

type metadataCommittedSink interface {
	SubmitMetadata(context.Context, messageevents.MessageCommitted) error
}

func combineCommittedSinks(sinks ...message.CommittedSink) message.CommittedSink {
	group := committedSinkGroup{}
	for _, sink := range sinks {
		if sink != nil {
			group = append(group, sink)
		}
	}
	if len(group) == 0 {
		return nil
	}
	return group
}

func (g committedSinkGroup) Submit(ctx context.Context, event messageevents.MessageCommitted) error {
	var firstErr error
	for _, sink := range g {
		if sink == nil {
			continue
		}
		if err := submitCommittedEvent(ctx, sink, event); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (g committedSinkGroup) RequiresCommittedPayload() bool {
	for _, sink := range g {
		if sink == nil {
			continue
		}
		policy, ok := sink.(message.CommittedPayloadPolicy)
		if !ok || policy.RequiresCommittedPayload() {
			return true
		}
	}
	return false
}

func submitCommittedEvent(ctx context.Context, sink message.CommittedSink, event messageevents.MessageCommitted) error {
	if metadataSink, ok := sink.(metadataCommittedSink); ok {
		event.Payload = nil
		event.MessageScopedUIDs = nil
		return metadataSink.SubmitMetadata(ctx, event)
	}
	return sink.Submit(ctx, event.Clone())
}
