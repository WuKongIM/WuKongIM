//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	appendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/stretchr/testify/require"
)

// Physical runtime deletion uses the real Slot FSM. The retained Channel log
// verifies that a stale prepared request is rejected after metadata recreation,
// while a request using the newly committed versions can continue the log.
func TestMQTTPreparedAppendRejectsPhysicalRuntimeRecreationSingleNodeCluster(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	a, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, a.Stop(ctx))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, a.Start(ctx))
	node := a.cluster.(*cluster.Node)
	id := ch.ChannelID{ID: "runtime-recreation", Type: 2}
	waitSingleNodeClusterRouteLeader(t, node, id.ID, cfg.NodeID)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	candidate := meta.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: int64(id.Type), Status: uint8(ch.StatusActive), ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Replicas: []uint64{cfg.NodeID}, ISR: []uint64{cfg.NodeID}, Leader: cfg.NodeID, MinISR: 1}
	create, err := fsm.EncodeCreateChannelRuntimeMetaBatchCommandChecked([]fsm.CreateChannelRuntimeMetaBatchItem{{HashSlot: node.HashSlotForKey(id.ID), Meta: candidate}})
	require.NoError(t, err)
	require.NoError(t, node.Propose(ctx, cluster.ProposeRequest{Key: id.ID, Command: create}))
	first, err := node.GetChannelRuntimeMetaFresh(ctx, id.ID, int64(id.Type))
	require.NoError(t, err)
	sourceQuery := meta.MQTTRead{Kind: meta.MQTTReadChannelRuntime, RuntimeChannel: meta.ChannelKey{ChannelID: id.ID, ChannelType: int64(id.Type)}}
	firstSource, err := node.ReadMQTT(ctx, sourceQuery)
	require.NoError(t, err)
	require.NotNil(t, firstSource.Runtime)
	require.Equal(t, first, *firstSource.Runtime.Meta)
	require.Zero(t, firstSource.Runtime.RetiredThrough)
	appender := clusterinfra.NewChannelAppender(node)
	q := appendcontract.AppendBatchRequest{ChannelID: appendcontract.ChannelID{ID: id.ID, Type: id.Type}, ExpectedEpoch: first.ChannelEpoch, ExpectedLeaderEpoch: first.LeaderEpoch, ExpectedRouteGeneration: first.RouteGeneration, CommitMode: appendcontract.CommitModeQuorum, Messages: []appendcontract.Message{{MessageID: a.messageIDs.Next(), Payload: []byte("old incarnation"), ServerTimestampMS: time.Now().UnixMilli()}}}
	oldID := q.Messages[0].MessageID
	r, err := appender.AppendBatch(ctx, q)
	require.NoError(t, err)
	require.Len(t, r.Items, 1)
	require.NoError(t, r.Items[0].Err)
	require.NoError(t, node.Propose(ctx, cluster.ProposeRequest{Key: id.ID, Command: fsm.EncodeDeleteChannelRuntimeMetaCommand(id.ID, int64(id.Type))}))
	_, err = node.GetChannelRuntimeMetaFresh(ctx, id.ID, int64(id.Type))
	require.ErrorIs(t, err, meta.ErrNotFound)
	deletedSource, err := node.ReadMQTT(ctx, sourceQuery)
	require.NoError(t, err)
	require.Nil(t, deletedSource.Runtime.Meta)
	require.Greater(t, deletedSource.Runtime.RetiredThrough, firstSource.Runtime.RetiredThrough)
	require.NoError(t, node.Propose(ctx, cluster.ProposeRequest{Key: id.ID, Command: create}))
	fresh, err := node.GetChannelRuntimeMetaFresh(ctx, id.ID, int64(id.Type))
	require.NoError(t, err)
	require.Greater(t, fresh.ChannelEpoch, first.ChannelEpoch)
	require.Greater(t, fresh.RouteGeneration, first.RouteGeneration)
	freshSource, err := node.ReadMQTT(ctx, sourceQuery)
	require.NoError(t, err)
	require.Equal(t, deletedSource.Runtime.RetiredThrough, freshSource.Runtime.RetiredThrough)
	require.Equal(t, fresh, *freshSource.Runtime.Meta)
	q.Messages = []appendcontract.Message{{MessageID: a.messageIDs.Next(), Payload: []byte("stale incarnation"), ServerTimestampMS: time.Now().UnixMilli()}}
	staleID := q.Messages[0].MessageID
	_, err = appender.AppendBatch(ctx, q)
	require.ErrorIs(t, err, appendcontract.ErrStaleRoute)
	q.ExpectedEpoch, q.ExpectedLeaderEpoch, q.ExpectedRouteGeneration = fresh.ChannelEpoch, fresh.LeaderEpoch, fresh.RouteGeneration
	q.Messages = []appendcontract.Message{{MessageID: a.messageIDs.Next(), Payload: []byte("new incarnation"), ServerTimestampMS: time.Now().UnixMilli()}}
	r, err = appender.AppendBatch(ctx, q)
	require.NoError(t, err)
	require.Len(t, r.Items, 1)
	require.NoError(t, r.Items[0].Err)
	for _, check := range []struct {
		id   uint64
		body string
	}{{oldID, "old incarnation"}, {staleID, ""}, {q.Messages[0].MessageID, "new incarnation"}} {
		pages, err := node.ReadChannelOriginalCommittedBatch(ctx, []channels.CommittedRead{{ChannelID: id, Request: store.ReadCommittedRequest{MessageID: check.id, Limit: 1, MaxBytes: 4096}}})
		require.NoError(t, err)
		require.Len(t, pages, 1)
		require.NoError(t, pages[0].Err)
		if check.body == "" {
			require.Empty(t, pages[0].Read.Messages)
		} else {
			require.Len(t, pages[0].Read.Messages, 1)
			require.Equal(t, []byte(check.body), pages[0].Read.Messages[0].Payload)
		}
	}
	t.Logf("mqtt_runtime_incarnation_evidence: nodes=1 hash_slots=256 physical_delete=true slot_fsm=true source_read_kind=22 coherent_retirement=true old_epoch=%d new_epoch=%d old_route=%d new_route=%d stale_request_absent=true committed_bodies=2", first.ChannelEpoch, fresh.ChannelEpoch, first.RouteGeneration, fresh.RouteGeneration)
}
