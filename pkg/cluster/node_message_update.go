package cluster

import (
	"context"
	"errors"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"time"
)

// ApplyMessageUpdate delegates a channel-owned mutation to the Slot FSM.
func (n *Node) ApplyMessageUpdate(ctx context.Context, q metadb.MessageUpdateMutation) (metadb.MessageUpdateMutationResult, error) {
	if err := n.ensureForeground(); err != nil {
		return metadb.MessageUpdateMutationResult{}, err
	}
	if n.defaultSlotProxy == nil {
		return metadb.MessageUpdateMutationResult{}, ErrNotStarted
	}
	if q.Op != "update" {
		epoch, err := n.messageContentEpoch(ctx)
		if err != nil {
			return metadb.MessageUpdateMutationResult{}, err
		}
		q.ExpectedContentEpoch = epoch
	}
	return n.defaultSlotProxy.ApplyMessageUpdate(ctx, q)
}

// ReadMessageUpdatesBatch reads latest content through authoritative Slot barriers.
func (n *Node) ReadMessageUpdatesBatch(ctx context.Context, q []metadb.MessageUpdateRead) ([]metadb.MessageUpdatePage, error) {
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	if n.defaultSlotProxy == nil {
		return nil, ErrNotStarted
	}
	return n.defaultSlotProxy.ReadMessageUpdatesBatch(ctx, q)
}

// overlayMessageContent changes only transient read results, never log records
// or replication payloads. The caller has already selected existing base rows.
func (n *Node) overlayMessageContent(ctx context.Context, messages []*channelruntime.Message) error {
	if len(messages) == 0 || n.defaultSlotProxy == nil {
		return nil
	}
	// Bound retained overlay bytes independently of the original read size.
	for start := 0; start < len(messages); start += metadb.MaxMessageUpdatePage {
		end := min(start+metadb.MaxMessageUpdatePage, len(messages))
		reads := make([]metadb.MessageUpdateRead, 0, end-start)
		groups := make(map[metadb.ChannelKey]int)
		for _, m := range messages[start:end] {
			key := metadb.ChannelKey{ChannelID: m.ChannelID, ChannelType: int64(m.ChannelType)}
			g, ok := groups[key]
			if !ok {
				g = len(reads)
				groups[key] = g
				reads = append(reads, metadb.MessageUpdateRead{ChannelID: key.ChannelID, ChannelType: key.ChannelType})
			}
			reads[g].IDs = append(reads[g].IDs, m.MessageID)
		}
		pages, err := n.ReadMessageUpdatesBatch(ctx, reads)
		if err != nil {
			return err
		}
		applyMessageUpdatePages(messages[start:end], pages, groups)
	}
	return nil
}

// applyMessageUpdatePages joins exact replacements to detached original rows.
// Identity and sequence must both match; unrelated/recreated records are ignored.
func applyMessageUpdatePages(messages []*channelruntime.Message, pages []metadb.MessageUpdatePage, groups map[metadb.ChannelKey]int) {
	var indexes []map[uint64]int
	anyUpdates := false
	for i, page := range pages {
		anyUpdates = anyUpdates || len(page.Updates) > 0
		if len(page.Updates) > 8 {
			if indexes == nil {
				indexes = make([]map[uint64]int, len(pages))
			}
			indexes[i] = make(map[uint64]int, len(page.Updates))
			for j := range page.Updates {
				indexes[i][page.Updates[j].MessageID] = j
			}
		}
	}
	if !anyUpdates {
		return
	}
	for _, m := range messages {
		group, ok := groups[metadb.ChannelKey{ChannelID: m.ChannelID, ChannelType: int64(m.ChannelType)}]
		if !ok {
			continue
		}
		rows := pages[group].Updates
		index := -1
		if len(rows) > 8 {
			if i, found := indexes[group][m.MessageID]; found {
				index = i
			}
		} else {
			// Small recent-message pages avoid per-channel maps and large struct copies.
			// The bound of eight keeps total matching work linear in the batch size.
			for i := len(rows) - 1; i >= 0; i-- {
				if rows[i].MessageID == m.MessageID {
					index = i
					break
				}
			}
		}
		if index < 0 {
			continue
		}
		row := &rows[index]
		if row.MessageSeq == m.MessageSeq {
			m.Payload = row.Payload
			m.Version = row.Version
			m.UpdatedAtMS = row.UpdatedAtMS
		}
	}
}

// overlayMessageReads keeps replacement growth inside the caller's page budget.
// A larger edited body shortens the page and preserves the next sequence.
func (n *Node) overlayMessageReads(ctx context.Context, reads []channels.CommittedRead, results []channels.CommittedReadResult) error {
	return overlayMessageReadResults(ctx, reads, results, n.overlayMessageContent)
}

// overlayMessageReadResults bounds replacement reads and preserves each source page.
func overlayMessageReadResults(ctx context.Context, reads []channels.CommittedRead, results []channels.CommittedReadResult, overlay func(context.Context, []*channelruntime.Message) error) error {
	if len(reads) != len(results) {
		return channelruntime.ErrInvalidConfig
	}
	type target struct {
		row, index int
		message    *channelruntime.Message
	}
	targets := make([]target, 0)
	for i := range results {
		if results[i].Err == nil {
			for j := range results[i].Read.Messages {
				targets = append(targets, target{i, j, &results[i].Read.Messages[j]})
			}
		}
	}
	used := make([]int, len(results))
	done := make([]bool, len(results))
	batchLimit := metadb.MaxMessageUpdatePage
	for start := 0; start < len(targets); {
		end := min(start+batchLimit, len(targets))
		chunk := make([]*channelruntime.Message, 0, end-start)
		for _, item := range targets[start:end] {
			if !done[item.row] {
				chunk = append(chunk, item.message)
			}
		}
		if len(chunk) == 0 {
			start = end
			continue
		}
		err := overlay(ctx, chunk)
		if errors.Is(err, metadb.ErrInvalidArgument) && len(chunk) > 7 {
			// Seven maximum-sized replacements fit the eight-MiB encoded reply bound.
			// Retry only this unconsumed chunk; no sequence or page state has advanced.
			batchLimit = 7
			continue
		}
		if err != nil {
			return err
		}
		for _, item := range targets[start:end] {
			if done[item.row] {
				continue
			}
			budget := reads[item.row].Request.MaxBytes
			if budget <= 0 || budget > metadb.MaxMessageUpdatePageBytes {
				budget = metadb.MaxMessageUpdatePageBytes
			}
			size := len(item.message.Payload)
			if used[item.row]+size > budget {
				truncateMessageUpdateRead(reads[item.row], &results[item.row], item.index)
				done[item.row] = true
				continue
			}
			used[item.row] += size
		}
		start = end
	}
	return nil
}
func truncateMessageUpdateRead(read channels.CommittedRead, result *channels.CommittedReadResult, count int) {
	if count == 0 {
		result.Err = metadb.ErrInvalidArgument
		result.Read.Messages = nil
		return
	}
	result.ContentTruncated = true
	last := result.Read.Messages[count-1].MessageSeq
	result.Read.Messages = result.Read.Messages[:count:count]
	if read.Request.Reverse {
		result.Read.NextSeq = last - 1
	} else {
		result.Read.NextSeq = last + 1
	}
}

// ListPendingMessageUpdates exposes bounded source work only while this node
// owns the hash slot; dispatch still revalidates through authoritative reads.
func (n *Node) ListPendingMessageUpdates(ctx context.Context, hs metadb.HashSlot, cursor metadb.MessageUpdatePendingCursor, limit int) ([]metadb.MessageUpdate, metadb.MessageUpdatePendingCursor, bool, error) {
	owned, err := n.IsLocalLeaderHashSlot(ctx, hs)
	if err != nil {
		return nil, cursor, false, err
	}
	if !owned {
		return nil, cursor, false, ErrNotLeader
	}
	return n.defaultSlotMetaDB.ForHashSlot(uint16(hs)).ListPendingMessageUpdates(ctx, cursor, limit)
}

// ListMessageUpdateRetentionCandidates scans body-free cleanup keys on the owner.
func (n *Node) ListMessageUpdateRetentionCandidates(ctx context.Context, hs metadb.HashSlot, cursor metadb.MessageUpdateRetentionCursor, limit int) ([]metadb.MessageUpdate, metadb.MessageUpdateRetentionCursor, bool, error) {
	owned, err := n.IsLocalLeaderHashSlot(ctx, hs)
	if err != nil {
		return nil, cursor, false, err
	}
	if !owned {
		return nil, cursor, false, ErrNotLeader
	}
	return n.defaultSlotMetaDB.ForHashSlot(uint16(hs)).ListMessageUpdateRetentionCandidates(ctx, cursor, limit)
}

// messageContentEpoch reads Controller state outside the restored metadata.
// The serving proposer invokes it under maintenance admission, including RPCs
// delayed across restore. Accepted proposals precede restore's Slot drain.
func (n *Node) messageContentEpoch(ctx context.Context) (uint64, error) {
	state, err := n.LocalState(ctx)
	if err != nil {
		return 0, err
	}
	if state.ScheduledBackup == nil {
		return 0, nil
	}
	return state.ScheduledBackup.ManagerSessionEpoch, nil
}

// ReadSlotBarrier confirms current local Slot leadership with a quorum read
// index and waits for durable apply without creating a metadata log entry.
func (n *Node) ReadSlotBarrier(ctx context.Context, slot multiraft.SlotID) error {
	if err := n.ensureForeground(); err != nil {
		return err
	}
	if n.defaultSlotRuntime == nil {
		return ErrNotStarted
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	release, err := n.acquireWriteAdmission()
	if err != nil {
		return err
	}
	defer release()
	return n.defaultSlotRuntime.ReadBarrier(ctx, slot)
}
