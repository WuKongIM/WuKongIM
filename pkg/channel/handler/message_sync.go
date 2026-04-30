package handler

import (
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
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
	if committedHW == 0 {
		return SyncMessagesResult{}, nil
	}
	minAvailableSeq := normalizeMinAvailableSeq(req.MinAvailableSeq)
	if committedHW < minAvailableSeq {
		return SyncMessagesResult{}, nil
	}
	if req.StartSeq == 0 && req.EndSeq == 0 {
		return syncLatestMessages(st, committedHW, minAvailableSeq, req.Limit)
	}
	if req.PullMode == SyncPullModeUp {
		return syncNextMessages(st, committedHW, minAvailableSeq, req)
	}
	return syncPreviousMessages(st, committedHW, minAvailableSeq, req)
}

func syncLatestMessages(st messageSyncStore, committedHW, minAvailableSeq uint64, limit int) (SyncMessagesResult, error) {
	messages, err := st.ListMessagesBySeq(committedHW, limit+1, math.MaxInt, true)
	if err != nil {
		return SyncMessagesResult{}, err
	}
	filtered := make([]channel.Message, 0, minInt(limit+1, len(messages)))
	for _, msg := range messages {
		if msg.MessageSeq == 0 || msg.MessageSeq > committedHW {
			continue
		}
		if msg.MessageSeq < minAvailableSeq {
			break
		}
		filtered = append(filtered, msg)
		if len(filtered) > limit {
			break
		}
	}
	hasMore := len(filtered) > limit
	if hasMore {
		filtered = filtered[:limit]
	}
	reverseMessages(filtered)
	return SyncMessagesResult{Messages: filtered, HasMore: hasMore}, nil
}

func syncNextMessages(st messageSyncStore, committedHW, minAvailableSeq uint64, req SyncMessagesRequest) (SyncMessagesResult, error) {
	startSeq := req.StartSeq
	if startSeq == 0 {
		startSeq = 1
	}
	if startSeq < minAvailableSeq {
		startSeq = minAvailableSeq
	}
	endExclusive := req.EndSeq
	maxExclusive := committedHW + 1
	if endExclusive == 0 || endExclusive > maxExclusive {
		endExclusive = maxExclusive
	}
	if startSeq >= endExclusive {
		return SyncMessagesResult{}, nil
	}

	messages, err := st.ListMessagesBySeq(startSeq, req.Limit+1, math.MaxInt, false)
	if err != nil {
		return SyncMessagesResult{}, err
	}
	filtered := make([]channel.Message, 0, minInt(req.Limit+1, len(messages)))
	for _, msg := range messages {
		if msg.MessageSeq == 0 || msg.MessageSeq > committedHW {
			continue
		}
		if msg.MessageSeq < minAvailableSeq {
			continue
		}
		if msg.MessageSeq >= endExclusive {
			break
		}
		filtered = append(filtered, msg)
		if len(filtered) > req.Limit {
			break
		}
	}
	hasMore := len(filtered) > req.Limit
	if hasMore {
		filtered = filtered[:req.Limit]
	}
	return SyncMessagesResult{Messages: filtered, HasMore: hasMore}, nil
}

func syncPreviousMessages(st messageSyncStore, committedHW, minAvailableSeq uint64, req SyncMessagesRequest) (SyncMessagesResult, error) {
	if req.StartSeq == 0 {
		return SyncMessagesResult{}, nil
	}
	startSeq := req.StartSeq
	if startSeq > committedHW {
		startSeq = committedHW
	}
	if req.EndSeq != 0 && req.EndSeq > startSeq {
		return SyncMessagesResult{}, nil
	}
	if startSeq < minAvailableSeq {
		return SyncMessagesResult{}, nil
	}
	endSeq := req.EndSeq
	if floorExclusive := minAvailableSeq - 1; endSeq < floorExclusive {
		endSeq = floorExclusive
	}

	messages, err := st.ListMessagesBySeq(startSeq, req.Limit+1, math.MaxInt, true)
	if err != nil {
		return SyncMessagesResult{}, err
	}
	filtered := make([]channel.Message, 0, minInt(req.Limit+1, len(messages)))
	for _, msg := range messages {
		if msg.MessageSeq == 0 || msg.MessageSeq > committedHW {
			continue
		}
		if msg.MessageSeq < minAvailableSeq {
			break
		}
		if endSeq != 0 && msg.MessageSeq <= endSeq {
			break
		}
		filtered = append(filtered, msg)
		if len(filtered) > req.Limit {
			break
		}
	}
	hasMore := len(filtered) > req.Limit
	if hasMore {
		filtered = filtered[:req.Limit]
	}
	reverseMessages(filtered)
	return SyncMessagesResult{Messages: filtered, HasMore: hasMore}, nil
}

func normalizeMinAvailableSeq(seq uint64) uint64 {
	if seq == 0 {
		return 1
	}
	return seq
}

func reverseMessages(messages []channel.Message) {
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
}
