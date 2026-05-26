package handler

import (
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	store "github.com/WuKongIM/WuKongIM/pkg/db/message"
)

// SyncPullMode selects the legacy channel message sync direction.
type SyncPullMode uint8

const (
	// SyncPullModeDown pulls older messages at or before StartSeq.
	SyncPullModeDown SyncPullMode = iota
	// SyncPullModeUp pulls newer messages at or after StartSeq.
	SyncPullModeUp
)

// SyncMessagesRequest configures one legacy-compatible channel message sync.
type SyncMessagesRequest struct {
	// ChannelID identifies the channel to scan.
	ChannelID channel.ChannelID
	// StartSeq is the inclusive starting sequence boundary.
	StartSeq uint64
	// EndSeq is the exclusive ending sequence boundary.
	EndSeq uint64
	// Limit is the maximum number of messages to return.
	Limit int
	// PullMode selects whether to scan older or newer messages.
	PullMode SyncPullMode
	// MinAvailableSeq is the first sequence that retention allows clients to read.
	MinAvailableSeq uint64
}

// SyncMessagesResult is one legacy-compatible channel message sync page.
type SyncMessagesResult struct {
	// Messages contains matched messages ordered by ascending message sequence.
	Messages []channel.Message
	// HasMore reports whether another page exists inside the requested bounds.
	HasMore bool
}

type messageSyncStore interface {
	ListMessagesBySeq(fromSeq uint64, limit int, maxBytes int, reverse bool) ([]channel.Message, error)
}

// SyncMessages scans channel messages using the historical /channel/messagesync
// range semantics while reading only committed channel log entries.
func SyncMessages(engine *store.Engine, committedHW uint64, req SyncMessagesRequest) (SyncMessagesResult, error) {
	if engine == nil || req.ChannelID.ID == "" || req.ChannelID.Type == 0 || req.Limit <= 0 {
		return SyncMessagesResult{}, channel.ErrInvalidArgument
	}
	if committedHW == 0 {
		return SyncMessagesResult{}, nil
	}
	st := engine.ForChannel(KeyFromChannelID(req.ChannelID), req.ChannelID)
	return syncMessagesFromStore(st, committedHW, req)
}

func syncMessagesFromStore(st messageSyncStore, committedHW uint64, req SyncMessagesRequest) (SyncMessagesResult, error) {
	if st == nil || req.ChannelID.ID == "" || req.ChannelID.Type == 0 || req.Limit <= 0 {
		return SyncMessagesResult{}, channel.ErrInvalidArgument
	}
	bounds, ok := newSeqBounds(committedHW, req.MinAvailableSeq)
	if !ok {
		return SyncMessagesResult{}, nil
	}
	if req.StartSeq == 0 && req.EndSeq == 0 {
		return syncLatestMessages(st, bounds, req.Limit)
	}
	if req.PullMode == SyncPullModeUp {
		return syncNextMessages(st, bounds, req)
	}
	return syncPreviousMessages(st, bounds, req)
}

func syncLatestMessages(st messageSyncStore, bounds seqBounds, limit int) (SyncMessagesResult, error) {
	messages, err := st.ListMessagesBySeq(bounds.max, limit+1, math.MaxInt, true)
	if err != nil {
		return SyncMessagesResult{}, err
	}
	filtered, hasMore := appendBoundedMessages(
		make([]channel.Message, 0, minInt(limit+1, len(messages))),
		messages,
		bounds,
		limit,
		seqDirectionReverse,
		nil,
	)
	filtered, _ = trimLimit(filtered, limit)
	reverseMessages(filtered)
	return SyncMessagesResult{Messages: filtered, HasMore: hasMore}, nil
}

func syncNextMessages(st messageSyncStore, bounds seqBounds, req SyncMessagesRequest) (SyncMessagesResult, error) {
	startSeq := req.StartSeq
	if startSeq == 0 || startSeq < bounds.min {
		startSeq = bounds.min
	}
	endExclusive := bounds.exclusiveUpper(true, req.EndSeq)
	if endExclusive != 0 && startSeq >= endExclusive {
		return SyncMessagesResult{}, nil
	}

	messages, err := st.ListMessagesBySeq(startSeq, req.Limit+1, math.MaxInt, false)
	if err != nil {
		return SyncMessagesResult{}, err
	}
	stop := func(msg channel.Message) bool {
		return endExclusive != 0 && msg.MessageSeq >= endExclusive
	}
	filtered, hasMore := appendBoundedMessages(
		make([]channel.Message, 0, minInt(req.Limit+1, len(messages))),
		messages,
		bounds,
		req.Limit,
		seqDirectionForward,
		stop,
	)
	filtered, _ = trimLimit(filtered, req.Limit)
	return SyncMessagesResult{Messages: filtered, HasMore: hasMore}, nil
}

func syncPreviousMessages(st messageSyncStore, bounds seqBounds, req SyncMessagesRequest) (SyncMessagesResult, error) {
	if req.StartSeq == 0 {
		return SyncMessagesResult{}, nil
	}
	startSeq := req.StartSeq
	if startSeq > bounds.max {
		startSeq = bounds.max
	}
	if req.EndSeq != 0 && req.EndSeq > startSeq {
		return SyncMessagesResult{}, nil
	}
	if startSeq < bounds.min {
		return SyncMessagesResult{}, nil
	}
	endSeq := req.EndSeq
	if floorExclusive := bounds.min - 1; endSeq < floorExclusive {
		endSeq = floorExclusive
	}

	messages, err := st.ListMessagesBySeq(startSeq, req.Limit+1, math.MaxInt, true)
	if err != nil {
		return SyncMessagesResult{}, err
	}
	stop := func(msg channel.Message) bool {
		return endSeq != 0 && msg.MessageSeq <= endSeq
	}
	filtered, hasMore := appendBoundedMessages(
		make([]channel.Message, 0, minInt(req.Limit+1, len(messages))),
		messages,
		bounds,
		req.Limit,
		seqDirectionReverse,
		stop,
	)
	filtered, _ = trimLimit(filtered, req.Limit)
	reverseMessages(filtered)
	return SyncMessagesResult{Messages: filtered, HasMore: hasMore}, nil
}
