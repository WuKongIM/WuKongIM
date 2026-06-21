package machine

import (
	"sort"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

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
	return s.ProposeAppendBatch(AppendBatchCommand{
		BatchOpID: cmd.OpID,
		Waiters: []AppendBatchWaiter{{
			OpID:       cmd.OpID,
			CommitMode: cmd.CommitMode,
			Records:    cmd.Records,
		}},
	})
}

// ProposeAppendBatch validates leader state and emits one durable batch append task.
func (s *ChannelState) ProposeAppendBatch(cmd AppendBatchCommand) Decision {
	if s.Status == ch.StatusDeleted || s.Status == ch.StatusDeleting {
		return Decision{Err: ch.ErrChannelNotFound}
	}
	if s.Role != ch.RoleLeader {
		return Decision{Err: ch.ErrNotLeader}
	}
	if !s.CommitReady {
		return Decision{Err: ch.ErrNotReady}
	}
	if s.InflightAppend != nil {
		return Decision{Err: ch.ErrNotReady}
	}
	waiters := make([]AppendBatchWaiter, 0, len(cmd.Waiters))
	recordCount := 0
	seen := make(map[ch.OpID]struct{}, len(cmd.Waiters))
	for _, waiter := range cmd.Waiters {
		if len(waiter.Records) == 0 {
			return Decision{Err: ch.ErrInvalidConfig}
		}
		if _, ok := seen[waiter.OpID]; ok {
			return Decision{Err: ch.ErrInvalidConfig}
		}
		seen[waiter.OpID] = struct{}{}
		if _, ok := s.PendingAppends[waiter.OpID]; ok {
			return Decision{Err: ch.ErrNotReady}
		}
		waiters = append(waiters, waiter)
		recordCount += len(waiter.Records)
	}
	if recordCount == 0 {
		return Decision{}
	}
	records := make([]ch.Record, 0, recordCount)
	waiterOpIDs := make([]ch.OpID, 0, len(waiters))
	waiterRecordCounts := make([]int, 0, len(waiters))
	for _, waiter := range waiters {
		mode := waiter.CommitMode
		if mode == 0 {
			mode = ch.CommitModeQuorum
		}
		cloned := cloneRecords(waiter.Records)
		records = append(records, cloned...)
		waiterOpIDs = append(waiterOpIDs, waiter.OpID)
		waiterRecordCounts = append(waiterRecordCounts, len(cloned))
		s.PendingAppends[waiter.OpID] = &AppendWaiter{OpID: waiter.OpID, CommitMode: mode, OmitResultPayload: waiter.OmitResultPayload, Records: cloned}
		s.appendPendingAppendOrder(waiter.OpID)
	}
	fence := ch.Fence{ChannelKey: s.Key, Generation: s.Generation, Epoch: s.Epoch, LeaderEpoch: s.LeaderEpoch, OpID: cmd.BatchOpID}
	s.InflightAppend = &AppendOp{OpID: cmd.BatchOpID, Records: records, WaiterOpIDs: waiterOpIDs, WaiterRecordCounts: waiterRecordCounts}
	return Decision{Tasks: []Task{{Kind: TaskKindStoreAppend, Fence: fence, StoreAppend: &StoreAppendTask{Records: records, Sync: true}}}}
}

// CancelAppendWaiter removes an append waiter that the client no longer observes.
func (s *ChannelState) CancelAppendWaiter(opID ch.OpID) bool {
	if s == nil {
		return false
	}
	if _, ok := s.PendingAppends[opID]; !ok {
		return false
	}
	delete(s.PendingAppends, opID)
	s.removePendingAppendOrder([]ch.OpID{opID})
	return true
}

// AbortAppendBatchProposal clears a still-inflight batch without completing its waiters.
func (s *ChannelState) AbortAppendBatchProposal(batchOpID ch.OpID) {
	if s.InflightAppend == nil || s.InflightAppend.OpID != batchOpID {
		return
	}
	for _, opID := range s.InflightAppend.WaiterOpIDs {
		delete(s.PendingAppends, opID)
	}
	s.removePendingAppendOrder(s.InflightAppend.WaiterOpIDs)
	s.InflightAppend = nil
}

// ApplyAppendStored publishes a fenced durable append result.
func (s *ChannelState) ApplyAppendStored(res AppendStoredResult) Decision {
	if !s.matchesInflightFence(res.Fence) {
		return Decision{}
	}
	if res.Err != nil {
		return s.failInflightAppend(res.Err)
	}
	inflight := s.InflightAppend
	s.assignStoredOffsets(inflight.Records, res.BaseOffset)
	s.assignInflightRecordsToWaiters(inflight)
	s.LEO = maxUint64(s.LEO, res.LastOffset)
	if s.Role == ch.RoleLeader {
		s.Progress[s.LocalNode] = ReplicaProgress{Match: s.LEO}
		s.AdvanceHW()
	}
	replyOrder := append([]ch.OpID(nil), inflight.WaiterOpIDs...)
	s.InflightAppend = nil
	decision := s.completeAppendWaiters(replyOrder)
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
	return s.completeAppendWaiters(s.pendingAppendOrder())
}

func (s *ChannelState) matchesInflightFence(fence ch.Fence) bool {
	return fence.ChannelKey == s.Key && fence.Generation == s.Generation && fence.Epoch == s.Epoch && fence.LeaderEpoch == s.LeaderEpoch && s.InflightAppend != nil && s.InflightAppend.OpID == fence.OpID
}

func (s *ChannelState) failInflightAppend(err error) Decision {
	if s.InflightAppend == nil {
		return Decision{}
	}
	replies := make([]Reply, 0, len(s.InflightAppend.WaiterOpIDs))
	completed := make([]ch.OpID, 0, len(s.InflightAppend.WaiterOpIDs))
	for _, opID := range s.InflightAppend.WaiterOpIDs {
		if _, ok := s.PendingAppends[opID]; !ok {
			continue
		}
		delete(s.PendingAppends, opID)
		completed = append(completed, opID)
		replies = append(replies, Reply{Kind: ReplyKindAppend, OpID: opID, Err: err})
	}
	s.removePendingAppendOrder(completed)
	s.InflightAppend = nil
	return Decision{Replies: replies}
}

func (s *ChannelState) assignStoredOffsets(records []ch.Record, base uint64) {
	for i := range records {
		records[i].Index = base + uint64(i)
		if records[i].Epoch == 0 {
			records[i].Epoch = s.Epoch
		}
	}
}

func (s *ChannelState) assignInflightRecordsToWaiters(inflight *AppendOp) {
	if inflight == nil {
		return
	}
	next := 0
	for i, opID := range inflight.WaiterOpIDs {
		count := 0
		if i < len(inflight.WaiterRecordCounts) {
			count = inflight.WaiterRecordCounts[i]
		}
		waiter := s.PendingAppends[opID]
		if waiter == nil {
			next += count
			continue
		}
		if count == 0 {
			count = len(waiter.Records)
		}
		end := next + len(waiter.Records)
		if count > 0 {
			end = next + count
		}
		if end > len(inflight.Records) {
			end = len(inflight.Records)
		}
		source := inflight.Records[next:end]
		if len(waiter.Records) == len(source) {
			assignStoredRecordMetadata(waiter.Records, source)
		} else {
			waiter.Records = cloneRecords(source)
		}
		if len(waiter.Records) > 0 {
			waiter.Target = waiter.Records[len(waiter.Records)-1].Index
		}
		next = end
	}
}

func assignStoredRecordMetadata(target, source []ch.Record) {
	for i := range source {
		target[i].ID = source[i].ID
		target[i].Index = source[i].Index
		target[i].Epoch = source[i].Epoch
		target[i].SizeBytes = source[i].SizeBytes
	}
}

func (s *ChannelState) completeAppendWaiters(order []ch.OpID) Decision {
	if len(s.PendingAppends) == 0 {
		return Decision{}
	}
	if len(order) == 0 {
		order = s.pendingAppendOrder()
		if len(order) == 0 {
			order = s.sortedPendingAppendOpIDs()
		}
	}
	replies := make([]Reply, 0, len(order))
	completed := make([]ch.OpID, 0, len(order))
	for _, opID := range order {
		waiter := s.PendingAppends[opID]
		if waiter == nil || waiter.Target == 0 {
			continue
		}
		if waiter.CommitMode == ch.CommitModeQuorum && s.HW < waiter.Target {
			continue
		}
		items := appendItemsForRecords(s.ID, waiter.Records, waiter.OmitResultPayload)
		reply := Reply{Kind: ReplyKindAppend, OpID: opID, AppendItems: items}
		if len(items) > 0 {
			reply.Append = items[0]
		}
		replies = append(replies, reply)
		delete(s.PendingAppends, opID)
		completed = append(completed, opID)
	}
	s.removePendingAppendOrder(completed)
	return Decision{Replies: replies}
}

func (s *ChannelState) pendingAppendOrder() []ch.OpID {
	if len(s.PendingAppendOrder) == 0 {
		return nil
	}
	order := make([]ch.OpID, len(s.PendingAppendOrder))
	copy(order, s.PendingAppendOrder)
	return order
}

func (s *ChannelState) sortedPendingAppendOpIDs() []ch.OpID {
	order := make([]ch.OpID, 0, len(s.PendingAppends))
	for opID := range s.PendingAppends {
		order = append(order, opID)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	return order
}

func (s *ChannelState) appendPendingAppendOrder(opID ch.OpID) {
	for _, pending := range s.PendingAppendOrder {
		if pending == opID {
			return
		}
	}
	s.PendingAppendOrder = append(s.PendingAppendOrder, opID)
}

func (s *ChannelState) removePendingAppendOrder(opIDs []ch.OpID) {
	if len(opIDs) == 0 || len(s.PendingAppendOrder) == 0 {
		return
	}
	remove := make(map[ch.OpID]struct{}, len(opIDs))
	for _, opID := range opIDs {
		remove[opID] = struct{}{}
	}
	kept := s.PendingAppendOrder[:0]
	for _, opID := range s.PendingAppendOrder {
		if _, ok := remove[opID]; ok {
			continue
		}
		kept = append(kept, opID)
	}
	s.PendingAppendOrder = kept
}

func appendItemsForRecords(id ch.ChannelID, records []ch.Record, omitPayload bool) []ch.AppendBatchItemResult {
	items := make([]ch.AppendBatchItemResult, len(records))
	for i, record := range records {
		msg := ch.Message{
			MessageID:         record.ID,
			MessageSeq:        record.Index,
			ChannelID:         id.ID,
			ChannelType:       id.Type,
			FromUID:           record.FromUID,
			ClientMsgNo:       record.ClientMsgNo,
			ServerTimestampMS: record.ServerTimestampMS,
			SyncOnce:          record.SyncOnce,
		}
		if !omitPayload {
			msg.Payload = cloneBytes(record.Payload)
		}
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
