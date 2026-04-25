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

func (t *raftTransport) Send(ctx context.Context, batch []multiraft.Envelope) error {
	for _, env := range batch {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		data, err := env.Message.Marshal()
		if err != nil {
			return err
		}
		body := encodeRaftBody(uint64(env.SlotID), data)
		// Individual send failures are silently skipped — the raft layer
		// handles retransmission. Only context cancellation is propagated.
		if err := t.client.Send(uint64(env.Message.To), uint64(env.SlotID), msgTypeRaft, body); err != nil {
			if t.logger != nil {
				t.logger.Warn("skip raft transport send after client error",
					wklog.Event("cluster.transport.raft_send.skipped"),
					wklog.NodeID(uint64(env.Message.From)),
					wklog.TargetNodeID(uint64(env.Message.To)),
					wklog.SlotID(uint64(env.SlotID)),
					wklog.Error(err),
				)
			}
		}
	}
	return nil
}
