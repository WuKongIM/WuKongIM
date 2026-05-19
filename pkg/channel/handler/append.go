package handler

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

const maxLegacyMessageSeq = uint64(^uint32(0))

func (s *service) Append(ctx context.Context, req channel.AppendRequest) (channel.AppendResult, error) {
	result, err := s.AppendBatch(ctx, channel.AppendBatchRequest{
		ChannelID:             req.ChannelID,
		Messages:              []channel.Message{req.Message},
		SupportsMessageSeqU64: req.SupportsMessageSeqU64,
		CommitMode:            req.CommitMode,
		ExpectedChannelEpoch:  req.ExpectedChannelEpoch,
		ExpectedLeaderEpoch:   req.ExpectedLeaderEpoch,
		TraceID:               req.TraceID,
		Attempt:               req.Attempt,
	})
	if err != nil {
		return channel.AppendResult{}, err
	}
	if len(result.Items) == 0 {
		return channel.AppendResult{}, nil
	}
	item := result.Items[0]
	if item.Err != nil {
		return channel.AppendResult{}, item.Err
	}
	return channel.AppendResult{MessageID: item.MessageID, MessageSeq: item.MessageSeq, Message: item.Message}, nil
}

func (s *service) AppendBatch(ctx context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	result := channel.AppendBatchResult{Items: make([]channel.AppendBatchItemResult, len(req.Messages))}
	if len(req.Messages) == 0 {
		return result, nil
	}

	key := s.channelKeyForID(req.ChannelID)
	meta, err := s.metaForKey(key)
	if err != nil {
		return channel.AppendBatchResult{}, err
	}
	if err := compatibleWithExpectation(meta, req.ExpectedChannelEpoch, req.ExpectedLeaderEpoch); err != nil {
		return channel.AppendBatchResult{}, err
	}
	switch meta.Status {
	case channel.StatusDeleting:
		return channel.AppendBatchResult{}, channel.ErrChannelDeleting
	case channel.StatusDeleted:
		return channel.AppendBatchResult{}, channel.ErrChannelNotFound
	}
	if meta.Features.MessageSeqFormat == channel.MessageSeqFormatU64 && !req.SupportsMessageSeqU64 {
		return channel.AppendBatchResult{}, channel.ErrProtocolUpgradeRequired
	}

	group, ok := s.cfg.Runtime.Channel(key)
	if !ok {
		return channel.AppendBatchResult{}, channel.ErrStaleMeta
	}
	state := group.Status()
	if state.Role != channel.ReplicaRoleLeader {
		return channel.AppendBatchResult{}, channel.ErrNotLeader
	}

	if req.CommitMode != 0 && req.CommitMode != channel.CommitModeQuorum {
		ctx = channel.WithCommitMode(ctx, req.CommitMode)
	}

	store := s.cfg.Store.ForChannel(key, req.ChannelID)
	newIndexes := make([]int, 0, len(req.Messages))
	drafts := make([]channel.Message, len(req.Messages))
	duplicateOf := make([]int, len(req.Messages))
	for i := range duplicateOf {
		duplicateOf[i] = -1
	}
	idempotentNew := make(map[channel.IdempotencyKey]int, len(req.Messages))
	for i, msg := range req.Messages {
		draft := msg
		draft.ChannelID = req.ChannelID.ID
		draft.ChannelType = req.ChannelID.Type
		drafts[i] = draft
		if draft.FromUID == "" || draft.ClientMsgNo == "" {
			newIndexes = append(newIndexes, i)
			continue
		}
		idKey := channel.IdempotencyKey{
			ChannelID:   req.ChannelID,
			FromUID:     draft.FromUID,
			ClientMsgNo: draft.ClientMsgNo,
		}
		stored, ok, err := resolveIdempotentAppendFromStore(store, idKey, draft)
		if err != nil {
			result.Items[i].Err = err
			continue
		}
		if ok {
			result.Items[i] = channel.AppendBatchItemResult{
				MessageID:  stored.MessageID,
				MessageSeq: stored.MessageSeq,
				Message:    stored.Message,
			}
			continue
		}
		if first, ok := idempotentNew[idKey]; ok {
			if hashPayload(drafts[first].Payload) != hashPayload(draft.Payload) {
				result.Items[i].Err = channel.ErrIdempotencyConflict
				continue
			}
			duplicateOf[i] = first
			continue
		}
		idempotentNew[idKey] = i
		newIndexes = append(newIndexes, i)
	}
	if len(newIndexes) == 0 {
		copyDuplicateAppendBatchResults(result.Items, duplicateOf)
		return result, nil
	}

	if meta.WriteFence.BlocksAppend() {
		for _, idx := range newIndexes {
			result.Items[idx].Err = channel.ErrWriteFenced
		}
		copyDuplicateAppendBatchResults(result.Items, duplicateOf)
		return result, nil
	}
	if meta.Features.MessageSeqFormat == channel.MessageSeqFormatLegacyU32 {
		if state.HW >= maxLegacyMessageSeq {
			for _, idx := range newIndexes {
				result.Items[idx].Err = channel.ErrMessageSeqExhausted
			}
			copyDuplicateAppendBatchResults(result.Items, duplicateOf)
			return result, nil
		}
		remaining := int(maxLegacyMessageSeq - state.HW)
		if remaining < len(newIndexes) {
			for _, idx := range newIndexes[remaining:] {
				result.Items[idx].Err = channel.ErrMessageSeqExhausted
			}
			newIndexes = newIndexes[:remaining]
		}
	}

	records := make([]channel.Record, 0, len(newIndexes))
	recordItemIndexes := make([]int, 0, len(newIndexes))
	for _, idx := range newIndexes {
		draft := drafts[idx]
		draft.MessageID = s.cfg.MessageIDs.Next()
		encoded, err := encodeMessage(draft)
		if err != nil {
			result.Items[idx].Err = err
			continue
		}
		drafts[idx] = draft
		records = append(records, channel.Record{
			ID:        draft.MessageID,
			Payload:   encoded,
			SizeBytes: len(encoded),
		})
		recordItemIndexes = append(recordItemIndexes, idx)
	}
	if len(records) == 0 {
		copyDuplicateAppendBatchResults(result.Items, duplicateOf)
		return result, nil
	}
	commit, err := group.Append(ctx, records)
	if err != nil {
		if errors.Is(err, channel.ErrNotLeader) || errors.Is(err, channel.ErrLeaseExpired) {
			return channel.AppendBatchResult{}, channel.ErrNotLeader
		}
		return channel.AppendBatchResult{}, err
	}

	currentMeta, err := s.metaForKey(key)
	if err != nil {
		return channel.AppendBatchResult{}, err
	}
	switch currentMeta.Status {
	case channel.StatusDeleting:
		return channel.AppendBatchResult{}, channel.ErrChannelDeleting
	case channel.StatusDeleted:
		return channel.AppendBatchResult{}, channel.ErrChannelNotFound
	}

	for recordIndex, itemIndex := range recordItemIndexes {
		messageSeq := commit.BaseOffset + uint64(recordIndex) + 1
		if meta.Features.MessageSeqFormat == channel.MessageSeqFormatLegacyU32 && messageSeq > maxLegacyMessageSeq {
			result.Items[itemIndex].Err = channel.ErrMessageSeqExhausted
			continue
		}
		committed := drafts[itemIndex]
		committed.MessageSeq = messageSeq
		result.Items[itemIndex] = channel.AppendBatchItemResult{
			MessageID:  committed.MessageID,
			MessageSeq: messageSeq,
			Message:    committed,
		}
	}
	copyDuplicateAppendBatchResults(result.Items, duplicateOf)
	return result, nil
}

func copyDuplicateAppendBatchResults(items []channel.AppendBatchItemResult, duplicateOf []int) {
	for i, first := range duplicateOf {
		if first >= 0 && first < len(items) {
			items[i] = items[first]
		}
	}
}

type appendIdempotencyStore interface {
	LookupIdempotency(key channel.IdempotencyKey) (channel.IdempotencyEntry, uint64, bool, error)
	GetMessageBySeq(seq uint64) (channel.Message, bool, error)
}

func resolveIdempotentAppendFromStore(store appendIdempotencyStore, key channel.IdempotencyKey, draft channel.Message) (channel.AppendResult, bool, error) {
	entry, payloadHash, ok, err := store.LookupIdempotency(key)
	if err != nil {
		return channel.AppendResult{}, false, err
	}
	if !ok {
		return channel.AppendResult{}, false, nil
	}
	if payloadHash != hashPayload(draft.Payload) {
		return channel.AppendResult{}, true, channel.ErrIdempotencyConflict
	}
	msg, ok, err := store.GetMessageBySeq(entry.MessageSeq)
	if err != nil {
		return channel.AppendResult{}, true, err
	}
	if !ok {
		return channel.AppendResult{}, true, channel.ErrStaleMeta
	}
	msg.MessageSeq = entry.MessageSeq
	return channel.AppendResult{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, Message: msg}, true, nil
}
