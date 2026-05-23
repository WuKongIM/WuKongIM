package machine

import ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"

// FetchCommand asks for a committed read view.
type FetchCommand struct {
	OpID     ch.OpID
	FromSeq  uint64
	Limit    int
	MaxBytes int
}

// ReadCommittedResult is the store completion for a committed read.
type ReadCommittedResult struct {
	Fence    ch.Fence
	Messages []ch.Message
	NextSeq  uint64
	Err      error
}

// BuildFetch captures the current committed frontier for a read.
func (s *ChannelState) BuildFetch(cmd FetchCommand) Decision {
	if s.Status == ch.StatusDeleted || s.Status == ch.StatusDeleting {
		return Decision{Err: ch.ErrChannelNotFound}
	}
	if !s.CommitReady {
		return Decision{Err: ch.ErrNotReady}
	}
	fromSeq := cmd.FromSeq
	if fromSeq == 0 {
		fromSeq = 1
	}
	if fromSeq > s.HW {
		next := s.HW + 1
		return Decision{Replies: []Reply{{Kind: ReplyKindFetch, OpID: cmd.OpID, Fetch: ch.FetchResult{NextSeq: next, CommittedSeq: s.HW}}}}
	}
	fence := ch.Fence{ChannelKey: s.Key, Generation: s.Generation, Epoch: s.Epoch, LeaderEpoch: s.LeaderEpoch, OpID: cmd.OpID}
	return Decision{Tasks: []Task{{Kind: TaskKindStoreReadCommitted, Fence: fence, ReadCommitted: &ReadCommittedTask{FromSeq: fromSeq, MaxSeq: s.HW, Limit: cmd.Limit, MaxBytes: cmd.MaxBytes}}}}
}

// ApplyReadCommitted completes a fetch after revalidating the result fence.
func (s *ChannelState) ApplyReadCommitted(res ReadCommittedResult) Decision {
	if res.Fence.ChannelKey != s.Key || res.Fence.Generation != s.Generation || res.Fence.Epoch != s.Epoch || res.Fence.LeaderEpoch != s.LeaderEpoch {
		return Decision{}
	}
	if res.Err != nil {
		return Decision{Replies: []Reply{{Kind: ReplyKindFetch, OpID: res.Fence.OpID, Err: res.Err}}}
	}
	messages := make([]ch.Message, 0, len(res.Messages))
	for _, msg := range res.Messages {
		if msg.MessageSeq <= s.HW {
			messages = append(messages, msg)
		}
	}
	next := res.NextSeq
	if next == 0 {
		next = s.HW + 1
	}
	return Decision{Replies: []Reply{{Kind: ReplyKindFetch, OpID: res.Fence.OpID, Fetch: ch.FetchResult{Messages: messages, NextSeq: next, CommittedSeq: s.HW}}}}
}
