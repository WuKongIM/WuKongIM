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
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

type mqttSourceNoPeers struct{}

func (mqttSourceNoPeers) Exchange(context.Context, ch.NodeID, replication.ExchangeBatch) (replication.ExchangeBatchResult, error) {
	return replication.ExchangeBatchResult{}, ch.ErrNotReady
}

func TestMQTTSourceServiceSingleNodeClusterOrderingAndRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	path := t.TempDir()
	meta := ch.Meta{Key: "1:mqtt-source-service", ID: ch.ChannelID{ID: "mqtt-source-service", Type: 1}, Epoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
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
	appendBusiness := func(id uint64, body string) ch.AppendResult {
		r, err := api.Append(ctx, ch.AppendRequest{ChannelID: meta.ID, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, Message: ch.Message{MessageID: id, FromUID: "sender", Payload: []byte(body), ServerTimestampMS: 1000 + int64(id)}})
		require.NoError(t, err)
		return r
	}
	// The exact reserved bytes in an ordinary message must remain business data.
	first := appendBusiness(10, quorumlog.MQTTSourceActivationPayload)
	require.Equal(t, uint64(1), first.MessageSeq)
	req := ch.MQTTSourceRequest{ChannelID: meta.ID, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, MessageID: 11, ServerTimestampMS: 1011}
	source, err := api.(ch.MQTTSourceActivator).EnsureMQTTSource(ctx, req)
	require.NoError(t, err)
	require.Equal(t, uint64(1), source.StartAfter)
	require.Equal(t, uint64(2), source.CommittedThrough)
	require.NotEmpty(t, source.Generation)
	after := appendBusiness(12, "after activation")
	require.Equal(t, uint64(3), after.MessageSeq)
	req.MessageID = 13
	repeat, err := api.(ch.MQTTSourceActivator).EnsureMQTTSource(ctx, req)
	require.NoError(t, err)
	require.Equal(t, source.Generation, repeat.Generation)
	require.Equal(t, source.StartAfter, repeat.StartAfter)
	require.Equal(t, after.MessageSeq, repeat.CommittedThrough, "repeat admission must not append a control")
	for _, modify := range []func(*ch.MQTTSourceRequest){
		func(r *ch.MQTTSourceRequest) { r.ExpectedChannelEpoch++ },
		func(r *ch.MQTTSourceRequest) { r.ExpectedLeaderEpoch++ },
		func(r *ch.MQTTSourceRequest) { r.ExpectedRouteGeneration++ },
	} {
		bad := req
		modify(&bad)
		_, err = api.(ch.MQTTSourceActivator).EnsureMQTTSource(ctx, bad)
		require.ErrorIs(t, err, ch.ErrStaleMeta)
	}
	closeAll()
	meta.LeaderEpoch++
	open()
	req.ExpectedLeaderEpoch = meta.LeaderEpoch
	recovered, err := api.(ch.MQTTSourceActivator).EnsureMQTTSource(ctx, req)
	require.NoError(t, err)
	require.Equal(t, source.Generation, recovered.Generation)
	require.Equal(t, source.StartAfter, recovered.StartAfter)
	require.GreaterOrEqual(t, recovered.CommittedThrough, repeat.CommittedThrough)
	lease, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	defer lease.Close()
	durable, found, err := lease.(store.MQTTSourceReader).LoadCommittedMQTTSource(ctx, recovered.CommittedThrough)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, recovered, durable)
	_, err = lease.AdoptRetentionBoundary(ctx, recovered.CommittedThrough, "committed")
	require.NoError(t, err)
	trim, err := lease.TrimMessagesThrough(ctx, recovered.CommittedThrough, store.RetentionTrimOptions{MaxMessages: 64, MaxBytes: 1 << 20})
	require.NoError(t, err)
	require.Equal(t, uint64(1), trim.DeletedThroughSeq, "source protection retains control and every later position")
	t.Log("mqtt_source_channel_evidence: single_node_cluster=true disk_store=true reactor_ordering=true native_payload_not_control=true repeat_without_append=true restart_recovery=true protected_trim=true product_listener=false")
}

func TestMQTTSourceServiceConcurrentFirstAdmission(t *testing.T) {
	factory := store.NewMessageDBFactory(t.TempDir())
	defer factory.Close()
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: factory, MaxBatchItems: 64, MaxBatchBytes: 4 << 20})
	require.NoError(t, err)
	runtime, err := replication.NewRuntime(replication.RuntimeConfig{LocalNode: 1, Store: adapter, Link: mqttSourceNoPeers{}, Goroutines: goruntimeregistry.New()})
	require.NoError(t, err)
	defer runtime.Close(context.Background())
	api, err := New(Config{LocalNode: 1, ReactorCount: 1, Store: factory, QuorumLog: runtime.Log()})
	require.NoError(t, err)
	defer api.Close()
	meta := ch.Meta{Key: "1:concurrent-source", ID: ch.ChannelID{ID: "concurrent-source", Type: 1}, Epoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	require.NoError(t, api.ApplyMeta(meta))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const callers = 16
	results := make([]ch.MQTTSourceSnapshot, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = api.(ch.MQTTSourceActivator).EnsureMQTTSource(ctx, ch.MQTTSourceRequest{ChannelID: meta.ID, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, MessageID: uint64(100 + i), ServerTimestampMS: 1000 + int64(i)})
		}(i)
	}
	wg.Wait()
	for i, result := range results {
		require.NoError(t, errs[i])
		require.Equal(t, results[0].Generation, result.Generation)
		require.Zero(t, result.StartAfter)
		require.Positive(t, result.CommittedThrough)
	}
}

func TestMQTTSourceServiceRejectsLegacyAndMissingFences(t *testing.T) {
	api, err := New(Config{LocalNode: 1, ReactorCount: 1, Store: store.NewMemoryFactory()})
	require.NoError(t, err)
	defer api.Close()
	meta := ch.Meta{Key: "1:legacy-source", ID: ch.ChannelID{ID: "legacy-source", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	require.NoError(t, api.ApplyMeta(meta))
	req := ch.MQTTSourceRequest{ChannelID: meta.ID, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, MessageID: 10, ServerTimestampMS: 1000}
	_, err = api.(ch.MQTTSourceActivator).EnsureMQTTSource(context.Background(), req)
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	for _, modify := range []func(*ch.MQTTSourceRequest){
		func(r *ch.MQTTSourceRequest) { r.ExpectedChannelEpoch = 0 }, func(r *ch.MQTTSourceRequest) { r.ExpectedLeaderEpoch = 0 }, func(r *ch.MQTTSourceRequest) { r.ExpectedRouteGeneration = 0 }, func(r *ch.MQTTSourceRequest) { r.MessageID = 0 }, func(r *ch.MQTTSourceRequest) { r.ServerTimestampMS = 0 },
	} {
		bad := req
		modify(&bad)
		_, err = api.(ch.MQTTSourceActivator).EnsureMQTTSource(context.Background(), bad)
		require.ErrorIs(t, err, ch.ErrInvalidConfig)
	}
}
