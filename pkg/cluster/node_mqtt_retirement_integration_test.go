//go:build integration

package cluster

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

// Consumer permission is controlled; selection, routing, quorum and restart use
// the real three-node cluster and its independent journals over TCP.
func verifyRoutedMQTTRetirement(t *testing.T, ctx context.Context, nodes []*Node, m ch.Meta, source ch.MQTTReplayPlanRequest, first, latest ch.MQTTReplayAnchorProof) (ch.MQTTReplayRetirementRequest, ch.MQTTReplayRetirementSelectionRequest) {
	t.Helper()
	q := ch.MQTTReplayRetirementSelectionRequest{Source: source, Captured: latest, Through: 3, Limit: 1}
	page, err := nodes[0].SelectChannelMQTTReplayRetirement(ctx, q)
	require.NoError(t, err)
	require.False(t, page.Done)
	require.False(t, page.HasCandidate)
	require.Equal(t, uint64(5), page.BeforeAnchor)
	q.BeforeAnchor = page.BeforeAnchor
	selected, err := nodes[1].SelectChannelMQTTReplayRetirement(ctx, q)
	require.NoError(t, err)
	require.True(t, selected.Done)
	require.Equal(t, first, selected.Candidate, "floor 3 must round down to the complete prefix through 2")
	request := ch.MQTTReplayRetirementRequest{Meta: m, Captured: latest, Candidate: selected.Candidate, ConsumerThrough: 3, MessageID: 920, ServerTimestampMS: 1020}
	proof, err := nodes[0].CommitChannelMQTTReplayRetirement(ctx, request)
	require.NoError(t, err)
	require.Equal(t, uint64(8), proof.Manifest.LastOffset)
	var wg sync.WaitGroup
	var results [4]ch.MQTTReplayRetirementProof
	var errs [4]error
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			retry := request.Clone()
			retry.MessageID += uint64(i + 1)
			call, done := context.WithTimeout(ctx, 3*time.Second)
			defer done()
			for attempt := 0; attempt < 32; attempt++ {
				results[i], errs[i] = nodes[i%3].CommitChannelMQTTReplayRetirement(call, retry)
				if !errors.Is(errs[i], ch.ErrNotReady) && !errors.Is(errs[i], ch.ErrBackpressured) {
					return
				}
				select {
				case <-call.Done():
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
		}(i)
	}
	wg.Wait()
	for i := range results {
		require.NoError(t, errs[i])
		require.Equal(t, proof, results[i])
	}
	advanced := request.Clone()
	advanced.Candidate = latest
	advanced.ConsumerThrough = 4
	advanced.MessageID = 930
	newer, err := nodes[1].CommitChannelMQTTReplayRetirement(ctx, advanced)
	require.NoError(t, err)
	require.Equal(t, uint64(9), newer.Manifest.LastOffset)
	old, err := nodes[0].CommitChannelMQTTReplayRetirement(ctx, request)
	require.NoError(t, err)
	require.Equal(t, newer, old)
	// An idle source may never append again. Every voter must learn that this
	// exact decision is committed so its next recovery turn can apply retirement.
	for _, node := range nodes {
		require.Eventually(t, func() bool {
			st, e := node.defaultChannelStore.ChannelStore(m.Key, m.ID)
			if e != nil {
				return false
			}
			defer st.Close()
			p, found, e := st.(channelstore.MQTTReplayLatestRetirementReader).LoadLatestMQTTReplayRetirement(ctx, source.Generation)
			return e == nil && found && p == newer
		}, 3*time.Second, 20*time.Millisecond, "idle voter %d must learn the committed retirement", node.cfg.NodeID)
	}
	// Reopen the exact serving node, then retry the older selection under the
	// same still-current authority. Historical journal proofs must remain sufficient.
	stopNodes(t, nodes[2])
	replacement, err := New(nodes[2].cfg)
	require.NoError(t, err)
	nodes[2] = replacement
	startNode(t, replacement)
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])
	recovered, err := nodes[0].CommitChannelMQTTReplayRetirement(ctx, request)
	require.NoError(t, err)
	require.Equal(t, newer, recovered)
	reselected, err := nodes[0].SelectChannelMQTTReplayRetirement(ctx, q)
	require.NoError(t, err)
	require.Equal(t, selected, reselected)
	t.Log("mqtt_retirement_routing_evidence: nodes=3 hash_slots=256 tcp=true disk=true historical_scan=true whole_anchor_round_down=true concurrent_retry=true advancing_decision=true old_retry=true serving_restart=true consumer_admission=controlled product_listener=false")
	return request, q
}
