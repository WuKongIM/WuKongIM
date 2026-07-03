package handler

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

func (s *service) Fetch(_ context.Context, req channel.FetchRequest) (channel.FetchResult, error) {
	if req.Limit <= 0 {
		return channel.FetchResult{}, channel.ErrInvalidFetchArgument
	}
	if req.MaxBytes <= 0 {
		return channel.FetchResult{}, channel.ErrInvalidFetchBudget
	}

	key := s.channelKeyForID(req.ChannelID)
	meta, err := s.metaForKey(key)
	if err != nil {
		return channel.FetchResult{}, err
	}
	if err := compatibleWithExpectation(meta, req.ExpectedChannelEpoch, req.ExpectedLeaderEpoch); err != nil {
		return channel.FetchResult{}, err
	}
	switch meta.Status {
	case channel.StatusDeleting:
		return channel.FetchResult{}, channel.ErrChannelDeleting
	case channel.StatusDeleted:
		return channel.FetchResult{}, channel.ErrChannelNotFound
	}

	group, ok := s.cfg.Runtime.Channel(key)
	if !ok {
		return channel.FetchResult{}, channel.ErrStaleMeta
	}
	state := group.Status()
	if !state.CommitReady {
		return channel.FetchResult{}, channel.ErrNotReady
	}
	committedSeq := state.HW
	minAvailableSeq := effectiveMinAvailableSeq(state)
	startSeq := req.FromSeq
	if startSeq == 0 || startSeq < minAvailableSeq {
		startSeq = minAvailableSeq
	}
	if startSeq > committedSeq {
		return channel.FetchResult{
			NextSeq:             startSeq,
			CommittedSeq:        committedSeq,
			MinAvailableSeq:     minAvailableSeq,
			RetentionThroughSeq: state.RetentionThroughSeq,
		}, nil
	}

	st := s.cfg.Store.ForChannel(key, req.ChannelID)
	result, err := fetchMessagesFromStore(st, committedSeq, startSeq, req.Limit, req.MaxBytes)
	if err != nil {
		return channel.FetchResult{}, err
	}
	result.MinAvailableSeq = minAvailableSeq
	result.RetentionThroughSeq = state.RetentionThroughSeq
	return result, nil
}

type fetchMessageStore interface {
	ListMessagesBySeq(fromSeq uint64, limit int, maxBytes int, reverse bool) ([]channel.Message, error)
}

func fetchMessagesFromStore(st fetchMessageStore, committedSeq, startSeq uint64, limit int, maxBytes int) (channel.FetchResult, error) {
	messages, err := st.ListMessagesBySeq(startSeq, limit, maxBytes, false)
	if err != nil {
		return channel.FetchResult{}, err
	}
	result := channel.FetchResult{
		Messages:     make([]channel.Message, 0, minInt(len(messages), limit)),
		NextSeq:      startSeq,
		CommittedSeq: committedSeq,
	}
	for _, msg := range messages {
		if msg.MessageSeq > committedSeq {
			break
		}
		result.Messages = append(result.Messages, msg)
		result.NextSeq = msg.MessageSeq + 1
	}
	return result, nil
}

func effectiveMinAvailableSeq(state channel.ReplicaState) uint64 {
	minAvailable := state.MinAvailableSeq
	computed := channel.EffectiveMinAvailableSeq(state.RetentionThroughSeq, state.LogStartOffset)
	if computed > minAvailable {
		minAvailable = computed
	}
	if minAvailable == 0 {
		return 1
	}
	return minAvailable
}
