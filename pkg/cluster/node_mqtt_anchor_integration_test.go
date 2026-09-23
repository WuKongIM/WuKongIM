//go:build integration

package cluster

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestMQTTAnchorThreeNodeRoutingRestartAndIsolation(t *testing.T) {
	voters := []ControlVoter{{NodeID: 1, Addr: freeTCPAddr(t)}, {NodeID: 2, Addr: freeTCPAddr(t)}, {NodeID: 3, Addr: freeTCPAddr(t)}}
	var nodes []*Node
	for _, v := range voters {
		cfg := Config{NodeID: v.NodeID, ListenAddr: v.Addr, DataDir: t.TempDir(), Control: ControlConfig{ClusterID: "mqtt-anchor-route", Voters: voters, AllowBootstrap: true}, Slots: SlotConfig{InitialSlotCount: 2, HashSlotCount: 256, ReplicaCount: 3}}
		cfg.HealthReport.Interval = 200 * time.Millisecond
		cfg.Channel.ReplicaCount = 3
		n, err := New(cfg)
		require.NoError(t, err)
		nodes = append(nodes, n)
	}
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	id := ch.ChannelID{ID: "mqtt-anchor-channel", Type: 2}
	m := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 3, Status: ch.StatusActive}
	install := func() {
		t.Helper()
		require.NoError(t, nodes[0].defaultSlotProxy.UpsertChannelRuntimeMeta(ctx, metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: 2, ChannelEpoch: m.Epoch,
			LeaderEpoch: m.LeaderEpoch, RouteGeneration: m.RouteGeneration, Leader: uint64(m.Leader), Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}, MinISR: int64(m.MinISR), Status: uint8(m.Status)}))
	}
	install()
	source, err := nodes[0].EnsureChannelMQTTSource(ctx, ch.MQTTSourceRequest{ChannelID: id, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, MessageID: 900, ServerTimestampMS: 1000})
	require.NoError(t, err)
	planRequest := func() ch.MQTTReplayPlanRequest {
		return ch.MQTTReplayPlanRequest{ChannelID: id, ExpectedChannelEpoch: m.Epoch, ExpectedLeaderEpoch: m.LeaderEpoch, ExpectedRouteGeneration: m.RouteGeneration, Generation: source.Generation}
	}
	planReplay := func() ch.MQTTReplayPlan {
		t.Helper()
		p, e := nodes[0].PlanChannelMQTTReplay(ctx, planRequest())
		require.NoError(t, e)
		return p
	}
	initialPlan := planReplay()
	require.False(t, initialPlan.HasAnchor)
	require.Equal(t, uint64(1), initialPlan.Source.CommittedThrough)
	appendBusiness := func(messageID, sequence uint64) {
		t.Helper()
		result, e := nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: messageID, FromUID: "sender", Payload: []byte("immutable business content"), ServerTimestampMS: 1001}})
		require.NoError(t, e)
		require.Equal(t, sequence, result.MessageSeq)
	}
	appendBusiness(901, 2)
	copyRange, more, err := planReplay().NextRange(256, 1<<20)
	require.NoError(t, err)
	require.True(t, more)
	rangeReq := ch.MQTTReplayRequest{ChannelID: id, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, Range: copyRange}
	copyPage := func(q ch.MQTTReplayRequest) ch.MQTTReplayCopyReceipt {
		t.Helper()
		var receipt ch.MQTTReplayCopyReceipt
		var e error
		require.Eventually(t, func() bool { receipt, e = nodes[0].CopyChannelMQTTReplay(ctx, q); return e == nil }, 5*time.Second, 20*time.Millisecond)
		return receipt
	}
	receipt := copyPage(rangeReq)
	request := ch.MQTTReplayAnchorRequest{Meta: m, Copy: receipt, MessageID: 902, ServerTimestampMS: 1002}
	proof, err := nodes[0].CommitChannelMQTTReplayAnchor(ctx, request)
	require.NoError(t, err)
	require.Equal(t, receipt.After, proof.Prefix())
	require.Equal(t, uint64(3), proof.Manifest.LastOffset)
	acceptedPlan := planReplay()
	require.True(t, acceptedPlan.HasAnchor)
	require.Equal(t, proof, acceptedPlan.Anchor)
	_, more, err = acceptedPlan.NextRange(256, 1<<20)
	require.NoError(t, err)
	require.False(t, more)
	// Concurrent exact retries reuse the single committed control over real TCP.
	var wg sync.WaitGroup
	var replies [4]ch.MQTTReplayAnchorProof
	var errs [4]error
	for i := range replies {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q := request.Clone()
			q.MessageID += uint64(i + 10)
			retryCtx, done := context.WithTimeout(ctx, 3*time.Second)
			defer done()
			for attempt := 0; attempt < 32; attempt++ {
				replies[i], errs[i] = nodes[i%3].CommitChannelMQTTReplayAnchor(retryCtx, q)
				// Concurrent metadata installation is intentionally try-lock bounded.
				// Retry only explicit temporary admission; never hide a proof conflict.
				if !errors.Is(errs[i], ch.ErrNotReady) && !errors.Is(errs[i], ch.ErrBackpressured) {
					return
				}
				select {
				case <-retryCtx.Done():
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
		}(i)
	}
	wg.Wait()
	for i := range replies {
		require.NoError(t, errs[i])
		require.Equal(t, proof, replies[i])
	}
	idleReq := rangeReq
	idleReq.Range.From = 3
	idleReq.Range.Through = 3
	idleReceipt := copyPage(idleReq)
	idle, err := nodes[0].CommitChannelMQTTReplayAnchor(ctx, ch.MQTTReplayAnchorRequest{Meta: m, Copy: idleReceipt, MessageID: 903, ServerTimestampMS: 1003})
	require.NoError(t, err)
	require.Equal(t, proof, idle)
	appendBusiness(904, 4)
	copyRange, more, err = planReplay().NextRange(256, 1<<20)
	require.NoError(t, err)
	require.True(t, more)
	require.Equal(t, uint64(3), copyRange.From)
	require.Equal(t, uint64(4), copyRange.Through)
	nextReq := rangeReq
	nextReq.Range = copyRange
	nextReceipt := copyPage(nextReq)
	// Preparation preserves a previously materialized short page. Consume that
	// page again with a larger bound only after the remaining suffix is copied.
	if nextReceipt.After.Through == 3 {
		rest := nextReq
		rest.Range.From = 4
		_ = copyPage(rest)
		nextReceipt = copyPage(nextReq)
	}
	require.Equal(t, uint64(4), nextReceipt.After.Through)
	next, err := nodes[0].CommitChannelMQTTReplayAnchor(ctx, ch.MQTTReplayAnchorRequest{Meta: m, Copy: nextReceipt, MessageID: 905, ServerTimestampMS: 1005})
	require.NoError(t, err)
	require.Equal(t, uint64(5), next.Manifest.LastOffset)
	// Restart the former serving node and elect a different Channel authority.
	stopNodes(t, nodes[1])
	replacement, err := New(nodes[1].cfg)
	require.NoError(t, err)
	nodes[1] = replacement
	startNode(t, replacement)
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])
	m.Leader = 3
	m.LeaderEpoch = 2
	m.RouteGeneration = 2
	m.MinISR = 2
	install()
	stale, err := nodes[0].CommitChannelMQTTReplayAnchor(ctx, request)
	require.Error(t, err)
	require.Zero(t, stale)
	rangeReq.ExpectedLeaderEpoch = 2
	rangeReq.ExpectedRouteGeneration = 2
	recoveredCopy := copyPage(rangeReq)
	recovered, err := nodes[0].CommitChannelMQTTReplayAnchor(ctx, ch.MQTTReplayAnchorRequest{Meta: m, Copy: recoveredCopy, MessageID: 906, ServerTimestampMS: 1006})
	require.NoError(t, err)
	require.Equal(t, proof, recovered, "old proof survives a later anchor, restart and authority change")
	recoveredPlan := planReplay()
	require.Equal(t, next, recoveredPlan.Anchor, "planning retains the latest accepted anchor after an older exact retry")
	// The new leader commits its native current-term barrier at position 6.
	appendBusiness(907, 7)
	route := waitRouteKeyLeaderConverged(t, nodes, id.ID)
	transferSlotLeaderAndWait(t, nodes, route.SlotID, 3)
	stopNodes(t, nodes[0], nodes[1])
	blocked, done := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer done()
	failed, err := nodes[2].CommitChannelMQTTReplayAnchor(blocked, ch.MQTTReplayAnchorRequest{Meta: m, Copy: recoveredCopy, MessageID: 908, ServerTimestampMS: 1008})
	require.Error(t, err)
	require.Zero(t, failed)
	planCtx, finish := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer finish()
	isolatedPlan, err := nodes[2].PlanChannelMQTTReplay(planCtx, planRequest())
	require.Error(t, err)
	require.Zero(t, isolatedPlan)
	t.Log("mqtt_plan_routing_evidence: fresh_slot=true captured_hw=true real_copy_and_commit=true accepted_prefix=true idle_no_work=true restart=true leader_change=true latest_survives_old_retry=true isolated_plan_rejected=true product_listener=false")
	t.Log("mqtt_anchor_routing_evidence: nodes=3 hash_slots=256 physical_slots=2 tcp=true disk=true real_copy_receipt=true remote_commit=true concurrent_retry=true idle_retry=true append_ordering=true restart=true leader_change=true old_proof=true isolated_reply_rejected=true source_release=false product_listener=false")
}
