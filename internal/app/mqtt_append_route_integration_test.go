//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	appendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMQTTPreparedAppendRejectsRecreatedDirectorySingleNodeCluster(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	a, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, a.Stop(ctx))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	require.NoError(t, a.Start(ctx))
	node := a.cluster.(*cluster.Node)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	channel := sessioncase.SourceChannel{ID: channelid.EncodePersonChannel("route-a", "route-b"), Type: 1}
	waitSingleNodeClusterRouteLeader(t, node, channel.ID, cfg.NodeID)
	admission, err := newMQTTInboxAdmission(node, a.messageIDs)
	require.NoError(t, err)
	prepare := func() sessioncase.InboxAdmissionProgress {
		require.Equal(t, []error{nil}, node.AdmitPersonDirectoryTasks(ctx, []meta.PersonDirectoryTask{{ChannelID: channel.ID, ChannelType: 1, CreatedAt: time.Now().UnixMilli()}}))
		var progress sessioncase.InboxAdmissionProgress
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var err error
			progress, err = admission.Advance(ctx, channel)
			require.NoError(c, err)
			require.True(c, progress.Ready)
		}, 10*time.Second, 25*time.Millisecond)
		return progress
	}
	first := prepare()
	m, err := node.GetChannelRuntimeMetaFresh(ctx, channel.ID, 1)
	require.NoError(t, err)
	appender := clusterinfra.NewChannelAppender(node)
	request := appendcontract.AppendBatchRequest{ChannelID: appendcontract.ChannelID{ID: channel.ID, Type: 1}, ExpectedEpoch: m.ChannelEpoch, ExpectedLeaderEpoch: m.LeaderEpoch, ExpectedRouteGeneration: m.RouteGeneration, CommitMode: appendcontract.CommitModeQuorum,
		Messages: []appendcontract.Message{{MessageID: a.messageIDs.Next(), Payload: []byte("before deletion"), ServerTimestampMS: time.Now().UnixMilli()}}}
	beforeID := request.Messages[0].MessageID
	result, err := appender.AppendBatch(ctx, request)
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.NoError(t, result.Items[0].Err)
	require.Equal(t, uint64(1), result.Items[0].MessageSeq)
	require.NoError(t, node.DeleteChannelMetadata(ctx, channel.ID, 1))
	second := prepare()
	require.Greater(t, second.Checkpoint.DirectoryGeneration, first.Checkpoint.DirectoryGeneration)
	current, err := node.GetChannelRuntimeMetaFresh(ctx, channel.ID, 1)
	require.NoError(t, err)
	require.Equal(t, m.ChannelEpoch, current.ChannelEpoch)
	require.Equal(t, m.LeaderEpoch, current.LeaderEpoch)
	request.Messages = []appendcontract.Message{{MessageID: a.messageIDs.Next(), Payload: []byte("stale preparation"), ServerTimestampMS: time.Now().UnixMilli()}}
	staleID := request.Messages[0].MessageID
	_, err = appender.AppendBatch(ctx, request)
	require.ErrorIs(t, err, appendcontract.ErrStaleRoute)
	require.Greater(t, current.RouteGeneration, m.RouteGeneration)
	request.ExpectedRouteGeneration = current.RouteGeneration
	request.Messages = []appendcontract.Message{{MessageID: a.messageIDs.Next(), Payload: []byte("after fresh preparation"), ServerTimestampMS: time.Now().UnixMilli()}}
	result, err = appender.AppendBatch(ctx, request)
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.NoError(t, result.Items[0].Err)
	require.Greater(t, result.Items[0].MessageSeq, uint64(1), "authority installation may add an internal barrier")
	for _, check := range []struct {
		id   uint64
		body string
	}{
		{beforeID, "before deletion"}, {request.Messages[0].MessageID, "after fresh preparation"}, {staleID, ""},
	} {
		pages, err := node.ReadChannelOriginalCommittedBatch(ctx, []channels.CommittedRead{{ChannelID: ch.ChannelID{ID: channel.ID, Type: 1}, Request: store.ReadCommittedRequest{MessageID: check.id, Limit: 1, MaxBytes: 4096}}})
		require.NoError(t, err)
		require.Len(t, pages, 1)
		require.NoError(t, pages[0].Err)
		page := pages[0].Read
		if check.id == staleID {
			require.Empty(t, page.Messages)
			continue
		}
		require.Len(t, page.Messages, 1)
		require.Equal(t, []byte(check.body), page.Messages[0].Payload)
	}
	t.Log("mqtt_prepared_append_evidence: nodes=1 hash_slots=256 real_directory_admission=true same_channel_epoch=true same_leader_epoch=true deleted_and_recreated=true stale_request_rejected=true committed_bodies=2 automatic_append_hook=false product_listener=false")
}
