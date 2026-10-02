//go:build integration

package service

import (
	"context"
	"strings"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

func TestWillReceiptServiceSingleNodeClusterTrimAndRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	path := t.TempDir()
	id := ch.ChannelID{ID: "will-receipt-service", Type: 2}
	meta := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	var factory *store.MessageDBFactory
	var runtime *replication.Runtime
	var api ch.Cluster
	open := func() {
		factory = store.NewMessageDBFactory(path)
		adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: factory, MaxBatchItems: 64, MaxBatchBytes: 4 << 20})
		require.NoError(t, err)
		runtime, err = replication.NewRuntime(replication.RuntimeConfig{LocalNode: 1, Store: adapter, Link: mqttSourceNoPeers{}, Goroutines: goruntimeregistry.New()})
		require.NoError(t, err)
		api, err = New(Config{LocalNode: 1, ReactorCount: 1, Store: factory, QuorumLog: runtime.Log()})
		require.NoError(t, err)
		require.NoError(t, api.ApplyMeta(meta))
	}
	closeAll := func() {
		if api != nil {
			require.NoError(t, api.Close())
			api = nil
		}
		if runtime != nil {
			require.NoError(t, runtime.Close(context.Background()))
			runtime = nil
		}
		if factory != nil {
			require.NoError(t, factory.Close())
			factory = nil
		}
	}
	t.Cleanup(closeAll)
	open()
	key := "mqtt-will-v1:" + strings.Repeat("a", 64)
	metadata, err := publication.Encode(publication.Metadata{Source: publication.SourceWill, QoS: 1, PublisherNamespace: "n", PublisherClientID: "c", OriginalTopic: "t", ServerWillKey: key})
	require.NoError(t, err)
	appended, err := api.Append(ctx, ch.AppendRequest{ChannelID: id, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, CommitMode: ch.CommitModeQuorum,
		Message: ch.Message{MessageID: 99, FromUID: "sender", ClientMsgNo: "original-client", Payload: []byte("will"), PublicationMetadata: metadata, ServerTimestampMS: 1000}})
	require.NoError(t, err)
	q := ch.WillReceiptRequest{ChannelID: id, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, FromUID: "sender", ServerWillKey: key}
	read := func() ch.WillReceiptResult {
		r, err := api.(ch.WillReceiptReader).ReadWillReceipt(ctx, q)
		require.NoError(t, err)
		require.True(t, r.Valid())
		require.True(t, r.Found)
		require.Equal(t, appended.MessageSeq, r.Receipt.MessageSeq)
		require.EqualValues(t, 99, r.Receipt.MessageID)
		require.EqualValues(t, 1000, r.Receipt.ServerTimestampMS)
		return r
	}
	before := read()
	lease, err := factory.ChannelStore(meta.Key, id)
	require.NoError(t, err)
	_, err = lease.AdoptRetentionBoundary(ctx, appended.MessageSeq, "committed")
	require.NoError(t, err)
	trim, err := lease.TrimMessagesThrough(ctx, appended.MessageSeq, store.RetentionTrimOptions{MaxMessages: 64, MaxBytes: 1 << 20})
	require.NoError(t, err)
	require.Equal(t, appended.MessageSeq, trim.DeletedThroughSeq)
	require.NoError(t, lease.Close())
	require.Equal(t, before.Receipt, read().Receipt)
	closeAll()
	meta.LeaderEpoch++
	meta.RouteGeneration++
	open()
	_, err = api.(ch.WillReceiptReader).ReadWillReceipt(ctx, q)
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	q.ExpectedLeaderEpoch = meta.LeaderEpoch
	q.ExpectedRouteGeneration = meta.RouteGeneration
	require.Equal(t, before.Receipt, read().Receipt)
	meta.RouteGeneration++
	meta.WriteFence = ch.WriteFence{Token: "migration", Version: 1}
	require.NoError(t, api.ApplyMeta(meta))
	q.ExpectedRouteGeneration = meta.RouteGeneration
	require.Equal(t, before.Receipt, read().Receipt, "stable write fence permits recovered immutable proof")
	q.ServerWillKey = "mqtt-will-v1:" + strings.Repeat("b", 64)
	absent, err := api.(ch.WillReceiptReader).ReadWillReceipt(ctx, q)
	require.NoError(t, err)
	require.False(t, absent.Found)
	require.Zero(t, absent.Receipt)
	t.Log("will_receipt_runtime_evidence: single_node_cluster=true quorum_runtime=true checkpointed_hw=true trim=true restart=true stable_fence=true stale_route_rejected=true publication_identity_preserved=true product_listener=false")
}
