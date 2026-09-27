package service

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

func TestAppendRouteFenceSingleCannotLoseProofInBatchConversion(t *testing.T) {
	factory := store.NewMemoryFactory()
	api, err := New(Config{LocalNode: 1, Store: factory, ReactorCount: 1})
	require.NoError(t, err)
	defer api.Close()
	m := ch.Meta{ID: ch.ChannelID{ID: "prepared", Type: 1}, Key: "1:prepared", Epoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	require.NoError(t, api.ApplyMeta(m))
	_, err = api.Append(context.Background(), ch.AppendRequest{ChannelID: m.ID, Message: ch.Message{MessageID: 1, Payload: []byte("body")}, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1})
	require.ErrorIs(t, err, ch.ErrInvalidConfig, "legacy storage has no durable route authority")
	require.Empty(t, readServiceStoreMessages(t, factory, m, 10))
}
