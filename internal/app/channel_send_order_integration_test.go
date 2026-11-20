//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

// A blocked cold directory must hold only its own Channel's SENDs. A second
// spelling of the same person Channel must keep the first Channel's order.
func TestGatewaySendOrdersByChannelOnOneConnection(t *testing.T) {
	// These peer order keys collide under both the 16-shard fixture and the
	// 4,000-shard production-scale replay. Both use the same connection.
	const sender, firstPeer, secondPeer = "order-sender", "order-peer-a", "order-peer-3334"
	firstChannel := channelid.EncodePersonChannel(sender, firstPeer)
	secondChannel := channelid.EncodePersonChannel(sender, secondPeer)
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	cfg.Cluster.Slots.InitialSlotCount = 12
	cfg.Gateway.Listeners = []gateway.ListenerOptions{{
		Name: "tcp", Network: "tcp", Address: "127.0.0.1:0", Transport: "gnet", Protocol: "wkproto",
	}}
	cfg.Gateway.Runtime.AsyncSendWorkers = 4
	cfg.Gateway.Runtime.AsyncSendQueueCapacity = 256
	cfg.Gateway.Session.AsyncSendBatchMaxRecords = 1
	cfg.Gateway.SetTokenAuthOn(false)
	a, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		require.NoError(t, a.Stop(stop))
	})
	node := a.cluster.(*cluster.Node)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	node.SetPersonDirectoryPreparation(func(ctx context.Context, tasks []metadb.PersonDirectoryTaskEntry) []error {
		for _, entry := range tasks {
			if entry.Task.ChannelID != firstChannel {
				continue
			}
			select {
			case entered <- struct{}{}:
			default:
			}
			pause, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			select {
			case <-release:
			case <-pause.Done():
				outcomes := make([]error, len(tasks))
				for i := range outcomes {
					outcomes[i] = pause.Err()
				}
				return outcomes
			}
			break
		}
		return a.personDirectoryProjector.Prepare(ctx, tasks)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	require.NoError(t, a.Start(ctx))
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	waitSingleNodeClusterRouteLeader(t, node, firstChannel, cfg.NodeID)
	waitSingleNodeClusterRouteLeader(t, node, secondChannel, cfg.NodeID)
	gw := a.gateway.(*gateway.Gateway)
	conn, err := client.New(client.Config{Addr: gw.ListenerAddr("tcp"), OperationTimeout: 5 * time.Second, AckTimeout: 5 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	_, err = conn.Connect(ctx, client.ConnectOptions{UID: sender, DeviceID: "order-device", DeviceFlag: frame.APP})
	require.NoError(t, err)

	send := func(target, messageNo string) *client.SendFuture {
		t.Helper()
		future, err := conn.SendAsync(ctx, client.Message{
			ChannelID: target, ChannelType: frame.ChannelTypePerson, ClientMsgNo: messageNo, Payload: []byte("channel order"),
		})
		require.NoError(t, err)
		return future
	}
	first := send(firstPeer, "first-a")
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first SEND never entered real directory preparation")
	}
	sameChannel := send(firstChannel, "second-a")
	otherChannel := send(secondPeer, "first-b")
	early, cancelEarly := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancelEarly()
	b, err := otherChannel.Wait(early)
	require.NoError(t, err, "B must receive its ACK while A is still waiting for directory preparation")
	require.Equal(t, "first-b", b.ClientMsgNo)
	require.EqualValues(t, 1, b.MessageSeq)
	draining, stopWaiting := context.WithCancel(ctx)
	stopWaiting()
	require.ErrorIs(t, gw.DrainSends(draining), context.Canceled)

	close(release)
	a1, err := first.Wait(ctx)
	require.NoError(t, err)
	a2, err := sameChannel.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, "first-a", a1.ClientMsgNo)
	require.Equal(t, "second-a", a2.ClientMsgNo)
	require.EqualValues(t, 1, a1.MessageSeq)
	require.EqualValues(t, 2, a2.MessageSeq)
	require.True(t, b.ObservedAt.Before(a1.ObservedAt))
	require.False(t, a2.ObservedAt.Before(a1.ObservedAt))
	require.NoError(t, gw.DrainSends(ctx))
	t.Log("B ACK preceded the paused A preparation; canonical aliases of A committed as sequence 1, then 2")
}
