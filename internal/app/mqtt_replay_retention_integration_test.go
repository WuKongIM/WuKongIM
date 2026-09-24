//go:build integration

package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func verifyMQTTReplayRetention(t *testing.T, ctx context.Context, nodes []*cluster.Node, prepared sessioncase.PreparedGroupSource, completed uint64, admitUnknown bool, ids interface{ Next() uint64 }, expected ch.MQTTReplayAnchorProof) {
	t.Helper()
	planner, err := newMQTTReplayRetention(nodes[1])
	require.NoError(t, err)
	plan, err := planner.Plan(ctx, prepared.Binding.Key.Owner)
	require.NoError(t, err)
	require.True(t, plan.Replay.HasAnchor)
	require.True(t, plan.HasConsumer)
	require.Equal(t, prepared.Binding.Key, plan.Consumer.Key)
	require.Equal(t, completed, plan.Consumer.CompletedThrough)
	require.Equal(t, min(completed, plan.Replay.Anchor.Anchor.Through), plan.Through)
	producer, err := newMQTTReplayRetirement(nodes[2], ids)
	require.NoError(t, err)
	var attempts, decisions, completions atomic.Int32
	var deferStop []*runtime.ReplayWorker
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, w := range deferStop {
			require.NoError(t, w.Stop(stop))
		}
	}()
	startWorkers := func() {
		for _, node := range nodes {
			w, e := newMQTTReplayWorker(node, ids, runtime.ReplayWorkerOptions{Interval: 20 * time.Millisecond, PagesPerTurn: 32, Observe: func(o runtime.ReplayObservation) {
				attempts.Add(int32(o.Attempts))
				decisions.Add(int32(o.RetirementCommits))
				completions.Add(int32(o.Completed))
			}})
			require.NoError(t, e)
			deferStop = append(deferStop, w)
			require.NoError(t, w.Start(ctx))
		}
	}
	if !admitUnknown {
		startWorkers()
		require.Eventually(t, func() bool { return attempts.Load() >= 6 }, 10*time.Second, 20*time.Millisecond)
		require.Zero(t, decisions.Load(), "background phases must preserve the ACK gap")
		gap, e := producer.Step(ctx, prepared.Binding.Key.Owner, sessioncase.ReplayRetirementCursor{})
		require.NoError(t, e)
		require.False(t, gap.Committed, "a real out-of-order ACK must not authorize retirement")
		return
	}
	// Controlled source registration models the durable first phase before
	// fresh Channel confirmation selects the new subscriber's starting boundary.
	unknown := prepared.Binding
	unknown.Key.ClientID += "-new"
	unknown.Revision, unknown.Stage = 1, meta.MQTTBindingPreparing
	unknown.BoundaryKnown, unknown.StartAfter, unknown.CompletedThrough = false, 0, 0
	unknown.ProgressRevision, unknown.ProtectionRevision = 0, 0
	unknown.UpdatedAtMS, unknown.RecoveryAtMS = time.Now().UnixMilli(), time.Now().UnixMilli()
	r, err := nodes[2].CompareAndSwapMQTTSourceBinding(ctx, 0, unknown)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, r.Status)
	blocked, err := planner.Plan(ctx, unknown.Key.Owner)
	require.NoError(t, err)
	require.True(t, blocked.HasConsumer)
	require.Equal(t, unknown.Key, blocked.Consumer.Key)
	require.Zero(t, blocked.Through)
	denied, err := producer.Step(ctx, unknown.Key.Owner, sessioncase.ReplayRetirementCursor{})
	require.NoError(t, err)
	require.Zero(t, denied, "unknown registration prevents retirement even after the previous consumer completed")
	startWorkers()
	require.Eventually(t, func() bool { return attempts.Load() >= 6 }, 10*time.Second, 20*time.Millisecond)
	require.Zero(t, decisions.Load(), "background retirement must observe the unknown obligation")
	// Fix the boundary only after the unknown responsibility was committed.
	unknown.Revision, unknown.BoundaryKnown, unknown.ProtectionRevision = 2, true, 2
	unknown.StartAfter, unknown.CompletedThrough = blocked.Replay.Source.CommittedThrough, blocked.Replay.Source.CommittedThrough
	r, err = nodes[2].CompareAndSwapMQTTSourceBinding(ctx, 1, unknown)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, r.Status)
	again, err := planner.Plan(ctx, unknown.Key.Owner)
	require.NoError(t, err)
	require.Equal(t, plan.Through, again.Through)
	require.GreaterOrEqual(t, unknown.StartAfter, plan.Through)
	// Only the managed worker may produce the first decision. Subsequent direct
	// calls below are retries, after the observed background commit completed.
	require.Eventually(t, func() bool { return decisions.Load() > 0 }, 10*time.Second, 20*time.Millisecond)
	completedAtCommit := completions.Load()
	require.Eventually(t, func() bool { return completions.Load() >= completedAtCommit+6 }, 10*time.Second, 20*time.Millisecond)
	decision, err := producer.Step(ctx, unknown.Key.Owner, sessioncase.ReplayRetirementCursor{})
	require.NoError(t, err)
	require.True(t, decision.Committed)
	// Clearing the migration fence may add native barriers and later anchors.
	// Consumer completion still authorizes exactly the original captured prefix.
	require.Equal(t, expected.Anchor, decision.Proof.Retirement.Anchor)
	require.Equal(t, expected.Manifest.LastOffset, decision.Proof.Retirement.AnchorPosition)
	remote, err := newMQTTReplayRetirement(nodes[0], ids)
	require.NoError(t, err)
	retry, err := remote.Step(ctx, unknown.Key.Owner, sessioncase.ReplayRetirementCursor{})
	require.NoError(t, err)
	require.True(t, retry.Committed)
	require.Equal(t, decision.Proof, retry.Proof)
	t.Log("mqtt_retirement_producer_evidence: nodes=3 hash_slots=256 ordered_consumer_plan=true real_ack_gap_blocks=true unknown_registration_blocks=true rounded_committed_decision=true cross_node_retry_identity=true automatic_retirement=true product_listener=false")
	t.Log("mqtt_retention_plan_evidence: anchor_before_consumer=true authoritative_minimum=true ack_gap_preserved=true unknown_registration_blocks=true new_boundary_after_registration=true physical_shared_gc=false")
}
