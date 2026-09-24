//go:build integration

package app

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/stretchr/testify/require"
)

// verifyMQTTSourceRemoval uses the real ended Session and distinct source Slot.
// The source acknowledgement remains an obligation until the next bounded turn.
func verifyMQTTSourceRemoval(t *testing.T, ctx context.Context, nodes []*cluster.Node, ended meta.MQTTSourceBinding) {
	t.Helper()
	removal, err := newMQTTSourceRemoval(nodes[0])
	require.NoError(t, err)
	ack, err := removal.Reconcile(ctx, ended.Key)
	require.NoError(t, err)
	require.True(t, ack.Changed)
	require.Equal(t, meta.MQTTBindingRemoving, ack.Binding.Stage)
	require.Equal(t, ended.Revision+1, ack.Binding.ProtectionRevision)
	retained, err := nodes[1].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceRetention, Owner: ended.Key.Owner, Limit: 64})
	require.NoError(t, err)
	require.Contains(t, retained.Bindings, ack.Binding)
	// Another coordinator resumes using only durable state, with no receipt handoff.
	resumed, err := newMQTTSourceRemoval(nodes[2])
	require.NoError(t, err)
	removed, err := resumed.Reconcile(ctx, ended.Key)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTBindingRemoved, removed.Binding.Stage)
	require.Equal(t, ack.Binding.ProtectionRevision, removed.Binding.ProtectionRevision)
	require.Equal(t, ended.ProgressRevision, removed.Binding.ProgressRevision)
	require.Zero(t, removed.Binding.RecoveryAtMS)
	stored, err := nodes[1].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: ended.Key})
	require.NoError(t, err)
	require.Equal(t, []meta.MQTTSourceBinding{removed.Binding}, stored.Bindings)
	retained, err = nodes[1].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceRetention, Owner: ended.Key.Owner, Limit: 64})
	require.NoError(t, err)
	for _, b := range retained.Bindings {
		require.NotEqual(t, ended.Key, b.Key)
	}
	again, err := removal.Reconcile(ctx, ended.Key)
	require.NoError(t, err)
	require.False(t, again.Changed)
	require.Equal(t, removed.Binding, again.Binding)
	key, err := proxy.MQTTSourceRoutingKey(ended.Key.Owner)
	require.NoError(t, err)
	route, err := nodes[0].RouteKey(key)
	require.NoError(t, err)
	discovered, err := nodes[1].ReadMQTTRecovery(ctx, route.HashSlot, meta.MQTTRead{Kind: meta.MQTTReadReplaySources, Limit: 64})
	require.NoError(t, err)
	require.Contains(t, discovered.SourceOwners, ended.Key.Owner)
	t.Log("mqtt_binding_removal_evidence: nodes=3 hash_slots=256 ended_session_real=true source_slot_ack=true retained_until_final_commit=true coordinator_1_to_3=true tombstone_retained=true replay_discovery_preserved=true product_listener=false")
}
