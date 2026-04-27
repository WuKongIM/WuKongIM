package raft

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	// msgTypeControllerRaft must not collide with cluster-level transport
	// message types because controller raft shares the same transport.Server.
	msgTypeControllerRaft uint8  = 3
	controllerShardKey    uint64 = 1
)

type raftTransport struct {
	// client sends controller Raft frames through the shared cluster transport pool.
	client *transport.Client
	// localNodeID identifies looped outbound frames that must never enter the network.
	localNodeID uint64
}

func newTransport(pool *transport.Pool, localNodeID uint64) *raftTransport {
	return &raftTransport{
		client:      transport.NewClient(pool),
		localNodeID: localNodeID,
	}
}

func (t *raftTransport) Close() {
	if t == nil || t.client == nil {
		return
	}
	t.client.Stop()
}

func (t *raftTransport) Send(ctx context.Context, messages []raftpb.Message) error {
	for _, msg := range messages {
		if err := ctx.Err(); err != nil {
			return err
		}
		if msg.To == 0 || msg.To == msg.From || (t.localNodeID != 0 && msg.To == t.localNodeID) {
			continue
		}
		data, err := msg.Marshal()
		if err != nil {
			return err
		}
		// Match multiraft transport semantics: transient send failures are not fatal
		// to the local raft loop and are retried by later raft traffic.
		_ = t.client.Send(uint64(msg.To), controllerShardKey, msgTypeControllerRaft, data)
	}
	return nil
}
