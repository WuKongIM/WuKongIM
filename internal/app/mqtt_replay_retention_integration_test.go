//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func verifyMQTTReplayRetention(t *testing.T, ctx context.Context, nodes []*cluster.Node, prepared sessioncase.PreparedGroupSource, completed uint64, admitUnknown bool) {
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
	if !admitUnknown {
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
	t.Log("mqtt_retention_plan_evidence: anchor_before_consumer=true authoritative_minimum=true ack_gap_preserved=true unknown_registration_blocks=true new_boundary_after_registration=true physical_shared_gc=false")
}
