package cluster

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/transport"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.etcd.io/raft/v3/raftpb"
)

// raftTransport adapts transport.Client to multiraft.Transport.
type raftTransport struct {
	client *transport.Client
	logger wklog.Logger
	// maxBodySize overrides the transport frame body limit in focused tests.
	maxBodySize int
	// nextSnapshotChunkID assigns chunk groups for oversized MsgSnap sends.
	nextSnapshotChunkID atomic.Uint64
}

// raftSendGroup collects slot-scoped raft messages that share a target node.
type raftSendGroup struct {
	fromNodeID uint64
	items      []raftBatchItem
}

func (t *raftTransport) Send(ctx context.Context, batch []multiraft.Envelope) error {
	if len(batch) == 0 {
		return nil
	}
	if len(batch) == 1 {
		return t.sendSingle(ctx, batch[0])
	}
	containsChunkedSnapshot, err := t.containsOversizedSnapshot(ctx, batch)
	if err != nil {
		return err
	}
	if containsChunkedSnapshot {
		for _, env := range batch {
			if err := t.sendSingle(ctx, env); err != nil {
				return err
			}
		}
		return nil
	}

	groups := make(map[uint64]*raftSendGroup)
	order := make([]uint64, 0, len(batch))
	for _, env := range batch {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		data, err := env.Message.Marshal()
		if err != nil {
			return err
		}
		targetNodeID := uint64(env.Message.To)
		group := groups[targetNodeID]
		if group == nil {
			group = &raftSendGroup{
				fromNodeID: uint64(env.Message.From),
			}
			groups[targetNodeID] = group
			order = append(order, targetNodeID)
		}
		group.items = append(group.items, raftBatchItem{
			slotID: uint64(env.SlotID),
			data:   data,
		})
	}
	for _, targetNodeID := range order {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		group := groups[targetNodeID]
		if len(group.items) == 0 {
			continue
		}
		firstSlotID := group.items[0].slotID
		msgType := msgTypeRaft
		body := encodeRaftBody(firstSlotID, group.items[0].data)
		if len(group.items) > 1 {
			msgType = msgTypeRaftBatch
			body = encodeRaftBatchBody(group.items)
		}
		// Send failures are silently skipped — the raft layer handles
		// retransmission. Only context cancellation is propagated.
		if err := t.client.Send(targetNodeID, firstSlotID, msgType, body); err != nil {
			t.logSkippedSend(group.fromNodeID, targetNodeID, firstSlotID, err)
		}
	}
	return nil
}

func (t *raftTransport) sendSingle(ctx context.Context, env multiraft.Envelope) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	data, err := env.Message.Marshal()
	if err != nil {
		return err
	}
	slotID := uint64(env.SlotID)
	targetNodeID := uint64(env.Message.To)
	body := encodeRaftBody(slotID, data)
	if len(body) > t.frameBodyLimit() && isRaftSnapshotMessage(env.Message) {
		return t.sendSnapshotChunks(ctx, env)
	}
	if err := t.client.Send(targetNodeID, slotID, msgTypeRaft, body); err != nil {
		t.logSkippedSend(uint64(env.Message.From), targetNodeID, slotID, err)
	}
	return nil
}

func (t *raftTransport) containsOversizedSnapshot(ctx context.Context, batch []multiraft.Envelope) (bool, error) {
	for _, env := range batch {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if !isRaftSnapshotMessage(env.Message) {
			continue
		}
		data, err := env.Message.Marshal()
		if err != nil {
			return false, err
		}
		if len(encodeRaftBody(uint64(env.SlotID), data)) > t.frameBodyLimit() {
			return true, nil
		}
	}
	return false, nil
}

func isRaftSnapshotMessage(msg raftpb.Message) bool {
	return msg.Type == raftpb.MsgSnap && msg.Snapshot != nil && len(msg.Snapshot.Data) > 0
}

func (t *raftTransport) sendSnapshotChunks(ctx context.Context, env multiraft.Envelope) error {
	msg := env.Message
	if !isRaftSnapshotMessage(msg) {
		return nil
	}
	header := msg
	snap := *msg.Snapshot
	snapshotData := snap.Data
	snap.Data = nil
	header.Snapshot = &snap
	headerData, err := header.Marshal()
	if err != nil {
		return err
	}
	bodyLimit := t.frameBodyLimit()
	chunkDataLimit := bodyLimit - raftSnapshotChunkHeaderSize - len(headerData)
	slotID := uint64(env.SlotID)
	targetNodeID := uint64(msg.To)
	if chunkDataLimit <= 0 {
		t.logSkippedSend(uint64(msg.From), targetNodeID, slotID, fmt.Errorf("%w: raft snapshot chunk header=%d limit=%d", transport.ErrMsgTooLarge, raftSnapshotChunkHeaderSize+len(headerData), bodyLimit))
		return nil
	}

	chunkID := t.nextSnapshotChunkID.Add(1)
	total := uint64(len(snapshotData))
	for offset := 0; offset < len(snapshotData); offset += chunkDataLimit {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		end := offset + chunkDataLimit
		if end > len(snapshotData) {
			end = len(snapshotData)
		}
		chunk := raftSnapshotChunk{
			slotID:  slotID,
			chunkID: chunkID,
			from:    uint64(msg.From),
			to:      uint64(msg.To),
			index:   msg.Snapshot.Metadata.Index,
			term:    msg.Snapshot.Metadata.Term,
			total:   total,
			offset:  uint64(offset),
			message: headerData,
			data:    snapshotData[offset:end],
		}
		body := encodeRaftSnapshotChunkBody(chunk)
		if len(body) > bodyLimit {
			t.logSkippedSend(uint64(msg.From), targetNodeID, slotID, fmt.Errorf("%w: raft snapshot chunk body=%d limit=%d", transport.ErrMsgTooLarge, len(body), bodyLimit))
			return nil
		}
		if err := t.client.Send(targetNodeID, slotID, msgTypeRaftSnapshotChunk, body); err != nil {
			t.logSkippedSend(uint64(msg.From), targetNodeID, slotID, err)
			return nil
		}
	}
	return nil
}

func (t *raftTransport) frameBodyLimit() int {
	if t != nil && t.maxBodySize > 0 {
		return t.maxBodySize
	}
	return transport.MaxMessageSize
}

func (t *raftTransport) logSkippedSend(fromNodeID, targetNodeID, slotID uint64, err error) {
	if t.logger == nil {
		return
	}
	t.logger.Warn("skip raft transport send after client error",
		wklog.Event("cluster.transport.raft_send.skipped"),
		wklog.NodeID(fromNodeID),
		wklog.TargetNodeID(targetNodeID),
		wklog.SlotID(slotID),
		wklog.Error(err),
	)
}
