package raft

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	msgTypeControllerRaft uint8  = 2
	controllerShardKey    uint64 = 1
)

type raftTransport struct {
	client *transport.Client
}

func newTransport(pool *transport.Pool) *raftTransport {
	return &raftTransport{
		client: transport.NewClient(pool),
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
