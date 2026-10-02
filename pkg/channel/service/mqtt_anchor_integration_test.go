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
	gr "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/stretchr/testify/require"
)

func TestMQTTAnchorServiceSingleNodeClusterOrderingRestartAndRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	path := t.TempDir()
	meta := ch.Meta{ID: ch.ChannelID{ID: "anchor-service", Type: 1}, Epoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	meta.Key = ch.ChannelKeyForID(meta.ID)
	var factory *store.MessageDBFactory
	var runtime *replication.Runtime
	var api ch.Cluster
	open := func() {
		factory = store.NewMessageDBFactory(path)
		st, e := replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: factory, MaxBatchItems: 64, MaxBatchBytes: 4 << 20})
		require.NoError(t, e)
		runtime, e = replication.NewRuntime(replication.RuntimeConfig{LocalNode: 1, Store: st, Link: mqttSourceNoPeers{}, Goroutines: gr.New()})
		require.NoError(t, e)
		api, e = New(Config{LocalNode: 1, ReactorCount: 1, Store: factory, QuorumLog: runtime.Log()})
		require.NoError(t, e)
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
	sourceReq := ch.MQTTSourceRequest{ChannelID: meta.ID, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, MessageID: 10, ServerTimestampMS: 10}
	source, e := api.(ch.MQTTSourceActivator).EnsureMQTTSource(ctx, sourceReq)
	require.NoError(t, e)
	planReplay := func() ch.MQTTReplayPlan {
		t.Helper()
		plan, err := api.(ch.MQTTReplayPlanner).PlanMQTTReplay(ctx, ch.MQTTReplayPlanRequest{ChannelID: meta.ID, ExpectedChannelEpoch: meta.Epoch, ExpectedLeaderEpoch: meta.LeaderEpoch, ExpectedRouteGeneration: meta.RouteGeneration, Generation: source.Generation})
		require.NoError(t, err)
		return plan
	}
	initial := planReplay()
	require.False(t, initial.HasAnchor)
	require.Equal(t, uint64(1), initial.Source.CommittedThrough)
	appended, e := api.Append(ctx, ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{MessageID: 11, ServerTimestampMS: 11, Payload: []byte("business")}})
	require.NoError(t, e)
	require.Equal(t, uint64(2), appended.MessageSeq)
	page, e := api.(ch.MQTTReplayPreparer).PrepareMQTTReplay(ctx, ch.MQTTReplayRequest{ChannelID: meta.ID, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, Range: ch.MQTTReplayRange{Generation: source.Generation, From: 1, Through: 2, Limit: 256, MaxBytes: 1 << 20}})
	require.NoError(t, e)
	receipt := ch.MQTTReplayCopyReceipt{Request: ch.MQTTReplayRequest{ChannelID: meta.ID, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, Range: ch.MQTTReplayRange{Generation: source.Generation, From: 1, Through: 2, Limit: len(page.Records), MaxBytes: int(page.After.TotalStoredBytes - page.Before.TotalStoredBytes)}}, Leader: 1, Authority: ch.MQTTReplayCopyAuthority(meta), WriteQuorum: 1, Copies: []ch.NodeID{1}, Before: page.Before, After: page.After}
	copied := planReplay()
	require.False(t, copied.HasAnchor, "local copy is not accepted progress")
	nextRange, more, e := copied.NextRange(256, 1<<20)
	require.NoError(t, e)
	require.True(t, more)
	require.Equal(t, uint64(1), nextRange.From)
	require.Equal(t, uint64(2), nextRange.Through)
	request := ch.MQTTReplayAnchorRequest{Meta: meta, Copy: receipt, MessageID: 12, ServerTimestampMS: 12}
	first, e := api.(ch.MQTTReplayAnchorCommitter).CommitMQTTReplayAnchor(ctx, request)
	require.NoError(t, e)
	require.Equal(t, uint64(3), first.Manifest.LastOffset)
	accepted := planReplay()
	require.True(t, accepted.HasAnchor)
	require.Equal(t, first, accepted.Anchor)
	_, more, e = accepted.NextRange(256, 1<<20)
	require.NoError(t, e)
	require.False(t, more, "a lone anchor tail has no new copy work")
	idleReq := receipt.Request
	idleReq.Range.From, idleReq.Range.Through = 3, 3
	idleReq.Range.Limit, idleReq.Range.MaxBytes = 256, 1<<20
	idlePage, e := api.(ch.MQTTReplayPreparer).PrepareMQTTReplay(ctx, idleReq)
	require.NoError(t, e)
	idle := request.Clone()
	idle.Copy.Request = idleReq
	idle.Copy.Request.Range.Limit = len(idlePage.Records)
	idle.Copy.Request.Range.MaxBytes = int(idlePage.After.TotalStoredBytes - idlePage.Before.TotalStoredBytes)
	idle.Copy.Before, idle.Copy.After = idlePage.Before, idlePage.After
	idle.MessageID = 13
	idleProof, e := api.(ch.MQTTReplayAnchorCommitter).CommitMQTTReplayAnchor(ctx, idle)
	require.NoError(t, e)
	require.Equal(t, first, idleProof)
	var wg sync.WaitGroup
	results := make([]ch.MQTTReplayAnchorProof, 8)
	errs := make([]error, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q := request
			q.MessageID = uint64(100 + i)
			results[i], errs[i] = api.(ch.MQTTReplayAnchorCommitter).CommitMQTTReplayAnchor(ctx, q)
		}(i)
	}
	wg.Wait()
	for i := range results {
		require.NoError(t, errs[i])
		require.Equal(t, first, results[i])
	}
	after, e := api.Append(ctx, ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{MessageID: 20, ServerTimestampMS: 20, Payload: []byte("after anchor")}})
	require.NoError(t, e)
	require.Equal(t, uint64(4), after.MessageSeq)
	nextPlan := planReplay()
	require.Equal(t, first, nextPlan.Anchor)
	nextRange, more, e = nextPlan.NextRange(256, 1<<20)
	require.NoError(t, e)
	require.True(t, more)
	require.Equal(t, uint64(3), nextRange.From)
	require.Equal(t, uint64(4), nextRange.Through)
	// Existing source confirmation exposes the reactor's committed frontier.
	confirmed, e := api.(ch.MQTTSourceActivator).EnsureMQTTSource(ctx, sourceReq)
	require.NoError(t, e)
	require.Equal(t, after.MessageSeq, confirmed.CommittedThrough)
	st, e := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, e)
	original, found, e := st.(store.ExactProposalLookup).LoadExactProposal(ctx, store.ExactProposalRequest{CommandID: first.Manifest.CommandID, MaxRecords: 1, MaxBytes: 1024})
	require.NoError(t, e)
	require.True(t, found)
	require.Equal(t, uint64(12), original.Records[0].ID)
	require.NoError(t, st.Close())
	bad := request
	bad.Meta.RouteGeneration++
	bad.Copy.Request.ExpectedRouteGeneration++
	bad.Copy.Authority = ch.MQTTReplayCopyAuthority(bad.Meta)
	_, e = api.(ch.MQTTReplayAnchorCommitter).CommitMQTTReplayAnchor(ctx, bad)
	require.ErrorIs(t, e, ch.ErrStaleMeta)
	closeAll()
	meta.LeaderEpoch++
	meta.RouteGeneration++
	open()
	recoveredPlan := planReplay()
	require.Equal(t, first, recoveredPlan.Anchor)
	require.GreaterOrEqual(t, recoveredPlan.Source.CommittedThrough, uint64(4))
	request.Meta = meta
	request.Copy.Request.ExpectedLeaderEpoch = meta.LeaderEpoch
	request.Copy.Request.ExpectedRouteGeneration = meta.RouteGeneration
	request.Copy.Authority = ch.MQTTReplayCopyAuthority(meta)
	request.MessageID = 999
	recovered, e := api.(ch.MQTTReplayAnchorCommitter).CommitMQTTReplayAnchor(ctx, request)
	require.NoError(t, e)
	require.Equal(t, first, recovered)
	t.Log("mqtt_anchor_reactor_evidence: single_node_cluster=true real_disk=true append_ordering=true concurrent_retry=true original_identity=true reactor_hw=true restart=true route_fenced=true fresh_slot_entry=false product_listener=false")
}

// Blocking happens at the real worker/deep-log seam, never on the reactor.
type mqttAnchorGate struct {
	replication.DurableQuorumLog
	entered, resume       chan struct{}
	enterOnce, resumeOnce sync.Once
}

func (g *mqttAnchorGate) release() { g.resumeOnce.Do(func() { close(g.resume) }) }
func (g *mqttAnchorGate) CommitMQTTReplayAnchor(ctx context.Context, q ch.MQTTReplayAnchorRequest) (ch.MQTTReplayAnchorProof, error) {
	g.enterOnce.Do(func() { close(g.entered) })
	select {
	case <-ctx.Done():
		return ch.MQTTReplayAnchorProof{}, ctx.Err()
	case <-g.resume:
	}
	return g.DurableQuorumLog.(ch.MQTTReplayAnchorCommitter).CommitMQTTReplayAnchor(ctx, q)
}

func TestMQTTAnchorServiceCancellationKeepsStartedDurability(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	factory := store.NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { require.NoError(t, factory.Close()) })
	adapter, e := replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: factory, MaxBatchItems: 64, MaxBatchBytes: 4 << 20})
	require.NoError(t, e)
	runtime, e := replication.NewRuntime(replication.RuntimeConfig{LocalNode: 1, Store: adapter, Link: mqttSourceNoPeers{}, Goroutines: gr.New()})
	require.NoError(t, e)
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })
	gate := &mqttAnchorGate{DurableQuorumLog: runtime.Log(), entered: make(chan struct{}), resume: make(chan struct{})}
	api, e := New(Config{LocalNode: 1, ReactorCount: 1, Store: factory, QuorumLog: gate})
	require.NoError(t, e)
	t.Cleanup(func() { require.NoError(t, api.Close()) })
	t.Cleanup(gate.release)
	meta := ch.Meta{ID: ch.ChannelID{ID: "anchor-cancel", Type: 1}, Epoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	meta.Key = ch.ChannelKeyForID(meta.ID)
	require.NoError(t, api.ApplyMeta(meta))
	sourceReq := ch.MQTTSourceRequest{ChannelID: meta.ID, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, MessageID: 1, ServerTimestampMS: 1}
	source, e := api.(ch.MQTTSourceActivator).EnsureMQTTSource(ctx, sourceReq)
	require.NoError(t, e)
	replayReq := ch.MQTTReplayRequest{ChannelID: meta.ID, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, Range: ch.MQTTReplayRange{Generation: source.Generation, From: 1, Through: 1, Limit: 256, MaxBytes: 1 << 20}}
	page, e := api.(ch.MQTTReplayPreparer).PrepareMQTTReplay(ctx, replayReq)
	require.NoError(t, e)
	replayReq.Range.Limit = len(page.Records)
	replayReq.Range.MaxBytes = int(page.After.TotalStoredBytes)
	request := ch.MQTTReplayAnchorRequest{Meta: meta, Copy: ch.MQTTReplayCopyReceipt{Request: replayReq, Leader: 1, Authority: ch.MQTTReplayCopyAuthority(meta), WriteQuorum: 1, Copies: []ch.NodeID{1}, Before: page.Before, After: page.After}, MessageID: 2, ServerTimestampMS: 2}
	caller, stop := context.WithCancel(ctx)
	defer stop()
	outcome := make(chan error, 1)
	go func() {
		_, err := api.(ch.MQTTReplayAnchorCommitter).CommitMQTTReplayAnchor(caller, request)
		outcome <- err
	}()
	select {
	case <-gate.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	stop()
	select {
	case err := <-outcome:
		require.ErrorIs(t, err, context.Canceled)
	case <-ctx.Done():
		t.Fatal("observer cancellation stalled reactor")
	}
	// The caller is done; retained copies must no longer alias this request.
	request.Meta.ISR[0] = 99
	request.Meta.Replicas[0] = 99
	request.Copy.Copies[0] = 99
	gate.release()
	business, e := api.Append(ctx, ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{MessageID: 3, ServerTimestampMS: 3, Payload: []byte("after canceled anchor")}})
	require.NoError(t, e)
	require.Equal(t, uint64(3), business.MessageSeq)
	current, e := api.(ch.MQTTSourceActivator).EnsureMQTTSource(ctx, sourceReq)
	require.NoError(t, e)
	require.Equal(t, uint64(3), current.CommittedThrough)
	st, e := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, e)
	defer st.Close()
	proof, found, e := st.(store.MQTTReplayAnchorReader).LoadMQTTReplayAnchor(ctx, 2)
	require.NoError(t, e)
	require.True(t, found)
	require.Equal(t, page.After, proof.Prefix())
	t.Log("mqtt_anchor_cancel_evidence: real_disk=true caller_canceled=true worker_continued=true metadata_owned=true durable_anchor=true subsequent_append_ordered=true reactor_hw=true product_listener=false")
}
