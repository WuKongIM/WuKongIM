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

type retirementServiceHarness struct {
	t       *testing.T
	ctx     context.Context
	path    string
	meta    ch.Meta
	factory *store.MessageDBFactory
	runtime *replication.Runtime
	api     ch.Cluster
}

func newRetirementServiceHarness(t *testing.T) *retirementServiceHarness {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	m := ch.Meta{ID: ch.ChannelID{ID: "retirement-service", Type: 1}, Epoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	m.Key = ch.ChannelKeyForID(m.ID)
	h := &retirementServiceHarness{t: t, ctx: ctx, path: t.TempDir(), meta: m}
	t.Cleanup(h.close)
	return h
}

func (h *retirementServiceHarness) open(wrap func(replication.DurableQuorumLog) replication.DurableQuorumLog) {
	t := h.t
	h.factory = store.NewMessageDBFactory(h.path)
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: h.factory, MaxBatchItems: 64, MaxBatchBytes: 4 << 20})
	require.NoError(t, err)
	h.runtime, err = replication.NewRuntime(replication.RuntimeConfig{LocalNode: 1, Store: adapter, Link: mqttSourceNoPeers{}, Goroutines: gr.New()})
	require.NoError(t, err)
	log := h.runtime.Log()
	if wrap != nil {
		log = wrap(log)
	}
	h.api, err = New(Config{LocalNode: 1, ReactorCount: 1, Store: h.factory, QuorumLog: log})
	require.NoError(t, err)
	require.NoError(t, h.api.ApplyMeta(h.meta))
}

func (h *retirementServiceHarness) close() {
	if h.api != nil {
		require.NoError(h.t, h.api.Close())
		h.api = nil
	}
	if h.runtime != nil {
		require.NoError(h.t, h.runtime.Close(context.Background()))
		h.runtime = nil
	}
	if h.factory != nil {
		require.NoError(h.t, h.factory.Close())
		h.factory = nil
	}
}

func (h *retirementServiceHarness) seed() ch.MQTTReplayRetirementRequest {
	t, ctx, m := h.t, h.ctx, h.meta
	source, err := h.api.(ch.MQTTSourceActivator).EnsureMQTTSource(ctx, ch.MQTTSourceRequest{ChannelID: m.ID, ExpectedChannelEpoch: m.Epoch, ExpectedLeaderEpoch: m.LeaderEpoch, ExpectedRouteGeneration: m.RouteGeneration, MessageID: 1, ServerTimestampMS: 1})
	require.NoError(t, err)
	_, err = h.api.Append(ctx, ch.AppendRequest{ChannelID: m.ID, Message: ch.Message{MessageID: 2, ServerTimestampMS: 2, Payload: []byte("business")}})
	require.NoError(t, err)
	q := ch.MQTTReplayRequest{ChannelID: m.ID, ExpectedChannelEpoch: m.Epoch, ExpectedLeaderEpoch: m.LeaderEpoch, ExpectedRouteGeneration: m.RouteGeneration, Range: ch.MQTTReplayRange{Generation: source.Generation, From: 1, Through: 2, Limit: 256, MaxBytes: 1 << 20}}
	page, err := h.api.(ch.MQTTReplayPreparer).PrepareMQTTReplay(ctx, q)
	require.NoError(t, err)
	q.Range.Limit, q.Range.MaxBytes = len(page.Records), int(page.After.TotalStoredBytes-page.Before.TotalStoredBytes)
	copy := ch.MQTTReplayCopyReceipt{Request: q, Leader: 1, Authority: ch.MQTTReplayCopyAuthority(m), WriteQuorum: 1, Before: page.Before, After: page.After, Copies: []ch.NodeID{1}}
	a, err := h.api.(ch.MQTTReplayAnchorCommitter).CommitMQTTReplayAnchor(ctx, ch.MQTTReplayAnchorRequest{Meta: m, Copy: copy, MessageID: 3, ServerTimestampMS: 3})
	require.NoError(t, err)
	require.Equal(t, uint64(3), a.Manifest.LastOffset)
	return ch.MQTTReplayRetirementRequest{Meta: m, Captured: a, Candidate: a, ConsumerThrough: 2, MessageID: 4, ServerTimestampMS: 4}
}

func TestMQTTRetirementServiceSingleNodeClusterOrderingRestartAndRetry(t *testing.T) {
	h := newRetirementServiceHarness(t)
	h.open(nil)
	q := h.seed()
	committer, ok := h.api.(ch.MQTTReplayRetirementCommitter)
	require.True(t, ok, "the service facade must own retirement admission")
	for _, mode := range []string{"route", "membership", "floor", "proof"} {
		bad := q.Clone()
		switch mode {
		case "route":
			bad.Meta.RouteGeneration++
		case "membership":
			bad.Meta.Replicas = []ch.NodeID{1, 2}
		case "floor":
			bad.ConsumerThrough--
		case "proof":
			bad.Captured.Anchor.Digest[0]++
			bad.Candidate = bad.Captured
		}
		p, err := committer.CommitMQTTReplayRetirement(h.ctx, bad)
		require.Error(t, err, mode)
		require.Zero(t, p, mode)
	}
	first, err := committer.CommitMQTTReplayRetirement(h.ctx, q)
	require.NoError(t, err)
	require.Equal(t, uint64(4), first.Manifest.LastOffset)
	var wg sync.WaitGroup
	var results [8]ch.MQTTReplayRetirementProof
	var errors [8]error
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			retry := q.Clone()
			retry.MessageID += uint64(i + 10)
			results[i], errors[i] = committer.CommitMQTTReplayRetirement(h.ctx, retry)
		}(i)
	}
	wg.Wait()
	for i := range results {
		require.NoError(t, errors[i])
		require.Equal(t, first, results[i])
	}
	after, err := h.api.Append(h.ctx, ch.AppendRequest{ChannelID: h.meta.ID, Message: ch.Message{MessageID: 5, ServerTimestampMS: 5, Payload: []byte("after retirement")}})
	require.NoError(t, err)
	require.Equal(t, uint64(5), after.MessageSeq)
	plan, err := h.api.(ch.MQTTReplayPlanner).PlanMQTTReplay(h.ctx, ch.MQTTReplayPlanRequest{ChannelID: h.meta.ID, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, Generation: q.Captured.Prefix().Generation})
	require.NoError(t, err)
	require.Equal(t, uint64(5), plan.Source.CommittedThrough)
	st, err := h.factory.ChannelStore(h.meta.Key, h.meta.ID)
	require.NoError(t, err)
	stored, found, err := st.(store.ExactProposalLookup).LoadExactProposal(h.ctx, store.ExactProposalRequest{CommandID: first.Manifest.CommandID, MaxRecords: 1, MaxBytes: 1024})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(4), stored.Records[0].ID)
	require.NoError(t, st.Close())
	h.close()
	h.meta.LeaderEpoch++
	h.meta.RouteGeneration++
	h.open(nil)
	q.Meta = h.meta
	q.MessageID = 999
	recovered, err := h.api.(ch.MQTTReplayRetirementCommitter).CommitMQTTReplayRetirement(h.ctx, q)
	require.NoError(t, err)
	require.Equal(t, first, recovered)
	t.Log("mqtt_retirement_reactor_evidence: single_node_cluster=true real_disk=true append_ordering=true concurrent_retry=true original_identity=true reactor_hw=true restart=true route_fenced=true consumer_admission=controlled product_listener=false")
}

type retirementServiceGate struct {
	replication.DurableQuorumLog
	entered, resume       chan struct{}
	enterOnce, resumeOnce sync.Once
}

func (g *retirementServiceGate) release() { g.resumeOnce.Do(func() { close(g.resume) }) }
func (g *retirementServiceGate) CommitMQTTReplayAnchor(ctx context.Context, q ch.MQTTReplayAnchorRequest) (ch.MQTTReplayAnchorProof, error) {
	return g.DurableQuorumLog.(ch.MQTTReplayAnchorCommitter).CommitMQTTReplayAnchor(ctx, q)
}
func (g *retirementServiceGate) CommitMQTTReplayRetirement(ctx context.Context, q ch.MQTTReplayRetirementRequest) (ch.MQTTReplayRetirementProof, error) {
	g.enterOnce.Do(func() { close(g.entered) })
	select {
	case <-ctx.Done():
		return ch.MQTTReplayRetirementProof{}, ctx.Err()
	case <-g.resume:
	}
	return g.DurableQuorumLog.(ch.MQTTReplayRetirementCommitter).CommitMQTTReplayRetirement(ctx, q)
}

func TestMQTTRetirementServiceCancellationKeepsStartedDurability(t *testing.T) {
	h := newRetirementServiceHarness(t)
	gate := &retirementServiceGate{entered: make(chan struct{}), resume: make(chan struct{})}
	t.Cleanup(gate.release)
	h.open(func(log replication.DurableQuorumLog) replication.DurableQuorumLog {
		gate.DurableQuorumLog = log
		return gate
	})
	q := h.seed()
	committer, ok := h.api.(ch.MQTTReplayRetirementCommitter)
	require.True(t, ok)
	caller, cancel := context.WithCancel(h.ctx)
	defer cancel()
	outcome := make(chan error, 1)
	go func() { _, err := committer.CommitMQTTReplayRetirement(caller, q); outcome <- err }()
	select {
	case <-gate.entered:
	case <-h.ctx.Done():
		t.Fatal(h.ctx.Err())
	}
	cancel()
	select {
	case err := <-outcome:
		require.ErrorIs(t, err, context.Canceled)
	case <-h.ctx.Done():
		t.Fatal("observer cancellation stalled reactor")
	}
	q.Meta.ISR[0], q.Meta.Replicas[0] = 99, 99
	gate.release()
	after, err := h.api.Append(h.ctx, ch.AppendRequest{ChannelID: h.meta.ID, Message: ch.Message{MessageID: 5, ServerTimestampMS: 5, Payload: []byte("after canceled retirement")}})
	require.NoError(t, err)
	require.Equal(t, uint64(5), after.MessageSeq)
	st, err := h.factory.ChannelStore(h.meta.Key, h.meta.ID)
	require.NoError(t, err)
	defer st.Close()
	require.NoError(t, st.StoreCheckpoint(h.ctx, ch.Checkpoint{HW: after.MessageSeq}))
	p, found, err := st.(store.MQTTReplayRetirementReader).LoadMQTTReplayRetirement(h.ctx, 4)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, q.Candidate.Anchor, p.Retirement.Anchor)
	t.Log("mqtt_retirement_cancel_evidence: real_disk=true caller_canceled=true worker_continued=true metadata_owned=true durable_retirement=true subsequent_append_ordered=true consumer_admission=controlled product_listener=false")
}
