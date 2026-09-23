//go:build integration

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/stretchr/testify/require"
)

func TestMQTTReplayServiceSingleNodeClusterPreparationAndRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	path := t.TempDir()
	meta := ch.Meta{Key: "2:replay-service", ID: ch.ChannelID{ID: "replay-service", Type: 2}, Epoch: 1, LeaderEpoch: 1, RouteGeneration: 1,
		Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
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
	source, err := api.(ch.MQTTSourceActivator).EnsureMQTTSource(ctx, ch.MQTTSourceRequest{ChannelID: meta.ID,
		ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, MessageID: 11, ServerTimestampMS: 1011})
	require.NoError(t, err)
	for i := uint64(12); i < 16; i++ {
		_, err := api.Append(ctx, ch.AppendRequest{ChannelID: meta.ID, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1,
			Message: ch.Message{MessageID: i, FromUID: "sender", Payload: []byte("replay body"), RedDot: true, Expire: 60, ServerTimestampMS: 1000 + int64(i)}})
		require.NoError(t, err)
	}
	req := ch.MQTTReplayRequest{ChannelID: meta.ID, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1,
		Range: ch.MQTTReplayRange{Generation: source.Generation, From: source.StartAfter + 1, Through: 5, Limit: 1, MaxBytes: 1024}}
	preparer := api.(ch.MQTTReplayPreparer)
	first, err := preparer.PrepareMQTTReplay(ctx, req)
	require.NoError(t, err)
	require.Equal(t, uint64(1), first.After.Through)
	// Concurrent retry calls must return the same existing short page, even when
	// the requested upper boundary reaches farther than current shared coverage.
	req.Range.Limit = 256
	const callers = 8
	results := make([]ch.MQTTReplayPage, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], errs[i] = preparer.PrepareMQTTReplay(ctx, req) }(i)
	}
	wg.Wait()
	for i := range results {
		require.NoError(t, errs[i])
		require.Equal(t, first, results[i])
	}
	req.Range.From = 2
	second, err := preparer.PrepareMQTTReplay(ctx, req)
	require.NoError(t, err)
	require.Equal(t, first.After, second.Before)
	require.Equal(t, uint64(5), second.After.Through)
	for _, change := range []func(*ch.MQTTReplayRequest){
		func(r *ch.MQTTReplayRequest) { r.ExpectedChannelEpoch++ }, func(r *ch.MQTTReplayRequest) { r.ExpectedLeaderEpoch++ },
		func(r *ch.MQTTReplayRequest) { r.ExpectedRouteGeneration++ }, func(r *ch.MQTTReplayRequest) { r.Range.Through++ },
	} {
		bad := req
		change(&bad)
		_, err := preparer.PrepareMQTTReplay(ctx, bad)
		require.Error(t, err)
	}
	closeAll()
	meta.LeaderEpoch++
	open()
	req.ExpectedLeaderEpoch = meta.LeaderEpoch
	recovered, err := api.(ch.MQTTReplayPreparer).PrepareMQTTReplay(ctx, req)
	require.NoError(t, err)
	require.Equal(t, second, recovered)
	clear(recovered.Records[0].Content)
	owned, err := api.(ch.MQTTReplayPreparer).PrepareMQTTReplay(ctx, req)
	require.NoError(t, err)
	require.Equal(t, second, owned)
	lease, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	defer lease.Close()
	_, err = lease.AdoptRetentionBoundary(ctx, 5, "copied")
	require.NoError(t, err)
	trim, err := lease.TrimMessagesThrough(ctx, 5, store.RetentionTrimOptions{MaxMessages: 64, MaxBytes: 1 << 20})
	require.NoError(t, err)
	require.Zero(t, trim.DeletedThroughSeq, "local replay preparation must not release the source")
	t.Log("mqtt_replay_admission_evidence: single_node_cluster=true quorum_runtime=true disk_restart=true concurrent_retry=true epoch_route_fences=true owned_content=true source_release_unchanged=true product_listener=false")
}
