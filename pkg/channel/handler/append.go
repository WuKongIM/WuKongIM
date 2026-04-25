package handler

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

const maxLegacyMessageSeq = uint64(^uint32(0))

func (s *service) Append(ctx context.Context, req channel.AppendRequest) (channel.AppendResult, error) {
	key := KeyFromChannelID(req.ChannelID)
	meta, err := s.metaForKey(key)
	if err != nil {
		return channel.AppendResult{}, err
	}
	if err := compatibleWithExpectation(meta, req.ExpectedChannelEpoch, req.ExpectedLeaderEpoch); err != nil {
		return channel.AppendResult{}, err
	}
	switch meta.Status {
	case channel.StatusDeleting:
		return channel.AppendResult{}, channel.ErrChannelDeleting
	case channel.StatusDeleted:
		return channel.AppendResult{}, channel.ErrChannelNotFound
	}
	if meta.Features.MessageSeqFormat == channel.MessageSeqFormatU64 && !req.SupportsMessageSeqU64 {
		return channel.AppendResult{}, channel.ErrProtocolUpgradeRequired
	}

	group, ok := s.cfg.Runtime.Channel(key)
	if !ok {
		return channel.AppendResult{}, channel.ErrStaleMeta
	}
	state := group.Status()
	if state.Role != channel.ReplicaRoleLeader {
		return channel.AppendResult{}, channel.ErrNotLeader
	}

	ctx = channel.WithCommitMode(ctx, req.CommitMode)
	draft := req.Message
	draft.ChannelID = req.ChannelID.ID
	draft.ChannelType = req.ChannelID.Type

	store := s.cfg.Store.ForChannel(key, req.ChannelID)
	if draft.FromUID != "" && draft.ClientMsgNo != "" {
		idKey := channel.IdempotencyKey{
			ChannelID:   req.ChannelID,
			FromUID:     draft.FromUID,
			ClientMsgNo: draft.ClientMsgNo,
		}
		result, ok, err := resolveIdempotentAppendFromStore(store, idKey, draft)
		if err != nil {
			return channel.AppendResult{}, err
		}
		if ok {
			return result, nil
		}
	}
	if meta.Features.MessageSeqFormat == channel.MessageSeqFormatLegacyU32 && state.HW >= maxLegacyMessageSeq {
		return channel.AppendResult{}, channel.ErrMessageSeqExhausted
	}

	draft.MessageID = s.cfg.MessageIDs.Next()
	encoded, err := encodeMessage(draft)
	if err != nil {
		return channel.AppendResult{}, err
	}

	commit, err := group.Append(ctx, []channel.Record{{
		Payload:   encoded,
		SizeBytes: len(encoded),
	}})
	if err != nil {
		if errors.Is(err, channel.ErrNotLeader) || errors.Is(err, channel.ErrLeaseExpired) {
			return channel.AppendResult{}, channel.ErrNotLeader
		}
		return channel.AppendResult{}, err
	}

	messageSeq := commit.BaseOffset + 1
	if meta.Features.MessageSeqFormat == channel.MessageSeqFormatLegacyU32 && messageSeq > maxLegacyMessageSeq {
		return channel.AppendResult{}, channel.ErrMessageSeqExhausted
	}
	currentMeta, err := s.metaForKey(key)
	if err != nil {
		return channel.AppendResult{}, err
	}
	switch currentMeta.Status {
	case channel.StatusDeleting:
		return channel.AppendResult{}, channel.ErrChannelDeleting
	case channel.StatusDeleted:
		return channel.AppendResult{}, channel.ErrChannelNotFound
	}

	committed := draft
	committed.MessageSeq = messageSeq
	return channel.AppendResult{MessageID: committed.MessageID, MessageSeq: messageSeq, Message: committed}, nil
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
