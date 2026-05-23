package machine

import ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"

// AppendCommand asks the leader state machine to append records.
type AppendCommand struct {
	OpID       ch.OpID
	CommitMode ch.CommitMode
	Records    []ch.Record
}

// AppendStoredResult is the durable leader append completion.
type AppendStoredResult struct {
	Fence      ch.Fence
	BaseOffset uint64
	LastOffset uint64
	Err        error
}

// FollowerAck reports follower replication progress to the leader.
type FollowerAck struct {
	Follower    ch.NodeID
	MatchOffset uint64
}

// ProposeAppend validates leader state and emits one durable append task.
func (s *ChannelState) ProposeAppend(cmd AppendCommand) Decision {
	if s.Status == ch.StatusDeleted || s.Status == ch.StatusDeleting {
		return Decision{Err: ch.ErrChannelNotFound}
	}
	if s.Role != ch.RoleLeader {
		return Decision{Err: ch.ErrNotLeader}
	}
	if !s.CommitReady {
		return Decision{Err: ch.ErrNotReady}
	}
	if len(cmd.Records) == 0 {
		return Decision{}
	}
	if s.InflightAppend != nil {
		return Decision{Err: ch.ErrNotReady}
	}
	mode := cmd.CommitMode
	if mode == 0 {
		mode = ch.CommitModeQuorum
	}
	records := cloneRecords(cmd.Records)
	fence := ch.Fence{ChannelKey: s.Key, Generation: s.Generation, Epoch: s.Epoch, LeaderEpoch: s.LeaderEpoch, OpID: cmd.OpID}
	s.InflightAppend = &AppendOp{OpID: cmd.OpID, Records: records}
	s.PendingAppends[cmd.OpID] = &AppendWaiter{OpID: cmd.OpID, CommitMode: mode, Records: records}
	return Decision{Tasks: []Task{{Kind: TaskKindStoreAppend, Fence: fence, StoreAppend: &StoreAppendTask{Records: records, Sync: true}}}}
}

// ApplyAppendStored publishes a fenced durable append result.
func (s *ChannelState) ApplyAppendStored(res AppendStoredResult) Decision {
	if res.Err != nil {
		return s.failAppend(res.Fence.OpID, res.Err)
	}
	if !s.matchesInflightFence(res.Fence) {
		return Decision{}
	}
	waiter := s.PendingAppends[res.Fence.OpID]
	if waiter == nil {
		s.InflightAppend = nil
		return Decision{}
	}
	s.assignStoredOffsets(waiter, res.BaseOffset)
	waiter.Target = res.LastOffset
	s.LEO = maxUint64(s.LEO, res.LastOffset)
	if s.Role == ch.RoleLeader {
		s.Progress[s.LocalNode] = ReplicaProgress{Match: s.LEO}
		s.AdvanceHW()
	}
	s.InflightAppend = nil
	decision := s.completeAppendWaiters()
	decision.Signals = append(decision.Signals, Signal{Kind: SignalKindReplicate})
	return decision
}

// ApplyFollowerAck updates leader-side follower match progress.
func (s *ChannelState) ApplyFollowerAck(ack FollowerAck) Decision {
	if s.Role != ch.RoleLeader || !s.IsReplica(ack.Follower) {
		return Decision{}
	}
	progress := s.Progress[ack.Follower]
	if ack.MatchOffset > progress.Match {
		progress.Match = ack.MatchOffset
		s.Progress[ack.Follower] = progress
	}
	s.AdvanceHW()
	return s.completeAppendWaiters()
}

func (s *ChannelState) matchesInflightFence(fence ch.Fence) bool {
	return fence.ChannelKey == s.Key && fence.Generation == s.Generation && fence.Epoch == s.Epoch && fence.LeaderEpoch == s.LeaderEpoch && s.InflightAppend != nil && s.InflightAppend.OpID == fence.OpID
}

func (s *ChannelState) failAppend(opID ch.OpID, err error) Decision {
	delete(s.PendingAppends, opID)
	if s.InflightAppend != nil && s.InflightAppend.OpID == opID {
		s.InflightAppend = nil
	}
	return Decision{Replies: []Reply{{Kind: ReplyKindAppend, OpID: opID, Err: err}}}
}

func (s *ChannelState) assignStoredOffsets(waiter *AppendWaiter, base uint64) {
	for i := range waiter.Records {
		waiter.Records[i].Index = base + uint64(i)
		if waiter.Records[i].Epoch == 0 {
			waiter.Records[i].Epoch = s.Epoch
		}
	}
}

func (s *ChannelState) completeAppendWaiters() Decision {
	if len(s.PendingAppends) == 0 {
		return Decision{}
	}
	replies := make([]Reply, 0, len(s.PendingAppends))
	for opID, waiter := range s.PendingAppends {
		if waiter.Target == 0 {
			continue
		}
		if waiter.CommitMode == ch.CommitModeQuorum && s.HW < waiter.Target {
			continue
		}
		items := appendItemsForRecords(s.ID, waiter.Records)
		reply := Reply{Kind: ReplyKindAppend, OpID: opID, AppendItems: items}
		if len(items) > 0 {
			reply.Append = items[0]
		}
		replies = append(replies, reply)
		delete(s.PendingAppends, opID)
	}
	return Decision{Replies: replies}
}

func appendItemsForRecords(id ch.ChannelID, records []ch.Record) []ch.AppendBatchItemResult {
	items := make([]ch.AppendBatchItemResult, len(records))
	for i, record := range records {
		msg := ch.Message{MessageID: record.ID, MessageSeq: record.Index, ChannelID: id.ID, ChannelType: id.Type, Payload: cloneBytes(record.Payload)}
		items[i] = ch.AppendBatchItemResult{MessageID: record.ID, MessageSeq: record.Index, Message: msg}
	}
	return items
}

func cloneRecords(in []ch.Record) []ch.Record {
	out := make([]ch.Record, len(in))
	copy(out, in)
	for i := range out {
		out[i].Payload = cloneBytes(out[i].Payload)
	}
	return out
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func maxUint64(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}
