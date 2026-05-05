package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// raftTransport adapts transport.Client to multiraft.Transport.
type raftTransport struct {
	client *transport.Client
	logger wklog.Logger
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
	if err := t.client.Send(targetNodeID, slotID, msgTypeRaft, body); err != nil {
		t.logSkippedSend(uint64(env.Message.From), targetNodeID, slotID, err)
	}
	return nil
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
