package handler

import (
	"errors"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	store "github.com/WuKongIM/WuKongIM/pkg/db/message"
)

// QueryMessagesRequest configures one channel-local message page scan.
type QueryMessagesRequest struct {
	// ChannelID identifies the channel to scan.
	ChannelID channel.ChannelID
	// BeforeSeq is the exclusive upper message sequence bound for the next page.
	BeforeSeq uint64
	// Limit is the maximum number of matched messages to return.
	Limit int
	// MessageID filters the result to one durable message identifier when set.
	MessageID uint64
	// ClientMsgNo filters the result to matching client message numbers when set.
	ClientMsgNo string
	// MinAvailableSeq is the first sequence that retention allows clients to read.
	MinAvailableSeq uint64
}

// QueryMessagesResult is the matched message page in descending sequence order.
type QueryMessagesResult struct {
	// Messages contains matched messages ordered from newest to oldest.
	Messages []channel.Message
	// HasMore reports whether another matched page exists.
	HasMore bool
	// NextBeforeSeq is the exclusive upper sequence bound for the next page.
	NextBeforeSeq uint64
}

// LoadCommittedHW returns the durable committed high watermark for one channel.
func LoadCommittedHW(engine *store.Engine, id channel.ChannelID) (uint64, error) {
	if engine == nil || id.ID == "" || id.Type == 0 {
		return 0, channel.ErrInvalidArgument
	}
	st := engine.ForChannel(KeyFromChannelID(id), id)
	checkpoint, err := st.LoadCheckpoint()
	if errors.Is(err, channel.ErrEmptyState) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return checkpoint.HW, nil
}

// QueryMessages scans one channel store page without materializing the full result set.
func QueryMessages(engine *store.Engine, committedHW uint64, req QueryMessagesRequest) (QueryMessagesResult, error) {
	if engine == nil {
		return QueryMessagesResult{}, channel.ErrInvalidArgument
	}
	if req.ChannelID.ID == "" || req.ChannelID.Type == 0 || req.Limit <= 0 {
		return QueryMessagesResult{}, channel.ErrInvalidArgument
	}
	if committedHW == 0 {
		return QueryMessagesResult{}, nil
	}
	st := engine.ForChannel(KeyFromChannelID(req.ChannelID), req.ChannelID)
	return queryMessagesFromStore(st, committedHW, req)
}

type messageQueryStore interface {
	GetMessageByMessageID(messageID uint64) (channel.Message, bool, error)
	ListMessagesByClientMsgNo(clientMsgNo string, beforeSeq uint64, limit int) ([]channel.Message, uint64, bool, error)
	ListMessagesBySeq(fromSeq uint64, limit int, maxBytes int, reverse bool) ([]channel.Message, error)
}

func queryMessagesFromStore(st messageQueryStore, committedHW uint64, req QueryMessagesRequest) (QueryMessagesResult, error) {
	if st == nil || req.ChannelID.ID == "" || req.ChannelID.Type == 0 || req.Limit <= 0 {
		return QueryMessagesResult{}, channel.ErrInvalidArgument
	}
	bounds, ok := newSeqBounds(committedHW, req.MinAvailableSeq)
	if !ok {
		return QueryMessagesResult{}, nil
	}
	if req.MessageID != 0 {
		return queryMessagesByMessageID(st, bounds, req)
	}
	if req.ClientMsgNo != "" {
		return queryMessagesByClientMsgNo(st, bounds, req)
	}
	return queryLatestMessages(st, bounds, req)
}

func queryMessagesByMessageID(st messageQueryStore, bounds seqBounds, req QueryMessagesRequest) (QueryMessagesResult, error) {
	msg, ok, err := st.GetMessageByMessageID(req.MessageID)
	if err != nil {
		return QueryMessagesResult{}, err
	}
	if !ok || msg.MessageSeq == 0 || !bounds.contains(msg.MessageSeq) {
		return QueryMessagesResult{}, nil
	}
	if req.BeforeSeq > 0 && msg.MessageSeq >= req.BeforeSeq {
		return QueryMessagesResult{}, nil
	}
	return QueryMessagesResult{Messages: []channel.Message{msg}}, nil
}

func queryMessagesByClientMsgNo(st messageQueryStore, bounds seqBounds, req QueryMessagesRequest) (QueryMessagesResult, error) {
	if req.BeforeSeq > 0 && req.BeforeSeq <= bounds.min {
		return QueryMessagesResult{}, nil
	}
	beforeSeq := bounds.exclusiveUpper(true, req.BeforeSeq)
	messages, nextBeforeSeq, hasMore, err := st.ListMessagesByClientMsgNo(req.ClientMsgNo, beforeSeq, req.Limit)
	if err != nil {
		return QueryMessagesResult{}, err
	}
	filtered := messages[:0]
	droppedBelowFloor := false
	for _, msg := range messages {
		if msg.MessageSeq == 0 || msg.MessageSeq > bounds.max {
			continue
		}
		if msg.MessageSeq < bounds.min {
			droppedBelowFloor = true
			continue
		}
		filtered = append(filtered, msg)
	}
	if nextBeforeSeq <= bounds.min || droppedBelowFloor {
		nextBeforeSeq = 0
		hasMore = false
	}
	return QueryMessagesResult{
		Messages:      filtered,
		HasMore:       hasMore,
		NextBeforeSeq: nextBeforeSeq,
	}, nil
}

func queryLatestMessages(st messageQueryStore, bounds seqBounds, req QueryMessagesRequest) (QueryMessagesResult, error) {
	startSeq := bounds.max
	if req.BeforeSeq > 0 {
		if req.BeforeSeq <= bounds.min {
			return QueryMessagesResult{}, nil
		}
		startSeq = req.BeforeSeq - 1
		if startSeq > bounds.max {
			startSeq = bounds.max
		}
	}
	if startSeq == 0 || startSeq < bounds.min {
		return QueryMessagesResult{}, nil
	}

	messages, err := st.ListMessagesBySeq(startSeq, req.Limit+1, math.MaxInt, true)
	if err != nil {
		return QueryMessagesResult{}, err
	}
	result := QueryMessagesResult{
		Messages: make([]channel.Message, 0, minInt(req.Limit, len(messages))),
	}
	for _, msg := range messages {
		if msg.MessageSeq == 0 || msg.MessageSeq > bounds.max {
			continue
		}
		if msg.MessageSeq < bounds.min {
			break
		}
		result.Messages = append(result.Messages, msg)
	}
	if len(result.Messages) <= req.Limit {
		return result, nil
	}
	result.HasMore = true
	result.NextBeforeSeq = result.Messages[req.Limit-1].MessageSeq
	if result.NextBeforeSeq <= bounds.min {
		result.HasMore = false
		result.NextBeforeSeq = 0
		return result, nil
	}
	result.Messages = result.Messages[:req.Limit]
	return result, nil
}
