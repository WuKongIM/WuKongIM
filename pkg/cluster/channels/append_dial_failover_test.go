package channels

import (
	"context"
	"fmt"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	nodetransport "github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/stretchr/testify/require"
)

type failoverDialForward struct {
	ForwardClient
	nodes      []ch.NodeID
	alwaysFail bool
}

func (f *failoverDialForward) ForwardAppend(ctx context.Context, node ch.NodeID, q ch.AppendRequest) (ch.AppendResult, error) {
	f.nodes = append(f.nodes, node)
	if node == 2 || f.alwaysFail {
		return ch.AppendResult{}, fmt.Errorf("%w: stopped leader", nodetransport.ErrDialFailed)
	}
	return ch.AppendResult{MessageID: q.Message.MessageID, MessageSeq: 7}, nil
}
func (f *failoverDialForward) ForwardAppendBatch(ctx context.Context, node ch.NodeID, q ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	single, err := f.ForwardAppend(ctx, node, ch.AppendRequest{ChannelID: q.ChannelID, Message: q.Messages[0]})
	if err != nil {
		return ch.AppendBatchResult{}, err
	}
	return ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{MessageID: single.MessageID, MessageSeq: single.MessageSeq}}}, nil
}

func TestServiceRefreshesCachedLeaderAfterDialFailure(t *testing.T) {
	for _, batch := range []bool{false, true} {
		for _, permanent := range []bool{false, true} {
			t.Run(fmt.Sprintf("batch=%t/permanent=%t", batch, permanent), func(t *testing.T) {
				id := ch.ChannelID{ID: "dead-leader", Type: 2}
				old := ch.Meta{ID: id, Key: ch.ChannelKeyForID(id), Epoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
				fresh := old
				fresh.Leader = 3
				fresh.LeaderEpoch++
				fresh.RouteGeneration++
				source := &countingMetaSource{metas: []ch.Meta{old, fresh}}
				forward := &failoverDialForward{alwaysFail: permanent}
				svc, err := NewService(Config{LocalNode: 1, Runtime: &benchRuntimeFake{}, MetaSource: source, Forward: forward})
				require.NoError(t, err)
				cached, err := svc.ResolveAppendAuthority(context.Background(), id)
				require.NoError(t, err)
				require.Equal(t, old, cached)
				if batch {
					_, err = svc.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{MessageID: 9}}})
				} else {
					_, err = svc.Append(context.Background(), ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: 9}})
				}
				if permanent {
					require.ErrorIs(t, err, nodetransport.ErrDialFailed)
				} else {
					require.NoError(t, err)
				}
				require.Equal(t, []ch.NodeID{2, 3}, forward.nodes, "one fresh resolve/retry, never a retry loop")
				require.Equal(t, 2, source.ensureCalls)
			})
		}
	}
}
