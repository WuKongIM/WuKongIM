package channelv2_test

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/stretchr/testify/require"
)

func TestPublicTypesCompile(t *testing.T) {
	meta := ch.Meta{
		Key:         ch.ChannelKey("1:demo"),
		ID:          ch.ChannelID{ID: "demo", Type: 1},
		Epoch:       2,
		LeaderEpoch: 3,
		Leader:      1,
		Replicas:    []ch.NodeID{1, 2, 3},
		ISR:         []ch.NodeID{1, 2, 3},
		MinISR:      2,
		LeaseUntil:  time.Now().Add(time.Second),
		Status:      ch.StatusActive,
	}
	var cluster ch.Cluster = nopCluster{}
	require.NoError(t, cluster.ApplyMeta(meta))
	_, err := cluster.Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID})
	require.NoError(t, err)
}

type nopCluster struct{}

func (nopCluster) ApplyMeta(ch.Meta) error { return nil }
func (nopCluster) Append(context.Context, ch.AppendRequest) (ch.AppendResult, error) {
	return ch.AppendResult{}, nil
}
func (nopCluster) AppendBatch(context.Context, ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	return ch.AppendBatchResult{}, nil
}
func (nopCluster) Tick(context.Context) error { return nil }
func (nopCluster) Close() error               { return nil }
