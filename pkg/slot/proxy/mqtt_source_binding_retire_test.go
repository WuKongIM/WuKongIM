package proxy

import (
	"context"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestMQTTSourceBindingRetireRoutesToOwnerAndFencesResurrection(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 256)
	s := nodes[0].store
	owner := metadb.MQTTBindingOwner{Kind: metadb.MQTTBindingChannel, ID: "2:group", Generation: "g"}
	r := metadb.MQTTSourceBinding{Key: metadb.MQTTSourceBindingKey{Owner: owner, Namespace: "main", ClientID: "client", SessionGeneration: 1, SubscriptionGeneration: 2}, UID: "alice", Topic: "topic", Revision: 1, IntentRevision: 2, AuthorizationVersion: 1, OperationID: "subscribe", Stage: metadb.MQTTBindingPreparing, RecoveryAtMS: 1000, UpdatedAtMS: 1000}
	first := r
	for expected, next := range []func(*metadb.MQTTSourceBinding){
		func(*metadb.MQTTSourceBinding) {},
		func(b *metadb.MQTTSourceBinding) {
			b.Revision, b.Stage, b.ReleaseReason, b.ProgressRevision = 2, metadb.MQTTBindingRemoving, metadb.MQTTBindingSessionEnded, 2
		},
		func(b *metadb.MQTTSourceBinding) {
			b.Revision, b.Stage, b.RecoveryAtMS, b.ProtectionRevision = 3, metadb.MQTTBindingRemoved, 0, 2
		},
	} {
		next(&r)
		res, err := s.CompareAndSwapMQTTSourceBinding(ctx, uint64(expected), r)
		require.NoError(t, err)
		require.Equal(t, metadb.MQTTSessionCASApplied, res.Status)
	}
	res, err := s.RetireMQTTSourceBinding(ctx, r.Key, 3, 1)
	require.NoError(t, err)
	require.Equal(t, metadb.MQTTSessionCASApplied, res.Status)
	res, err = s.RetireMQTTSourceBinding(ctx, r.Key, 3, 1)
	require.NoError(t, err)
	require.Equal(t, metadb.MQTTSessionCASConflict, res.Status)
	res, err = s.CompareAndSwapMQTTSourceBinding(ctx, 0, first)
	require.NoError(t, err)
	require.Equal(t, metadb.MQTTSessionCASConflict, res.Status, "retired lifetime must stay fenced")
	got, err := s.ReadMQTTRecovery(ctx, nodes[0].cluster.HashSlotForKey("group"), metadb.MQTTRead{Kind: metadb.MQTTReadReplaySources, Limit: 64})
	require.NoError(t, err)
	require.Equal(t, []metadb.MQTTBindingOwner{owner}, got.SourceOwners)
	_, err = s.RetireMQTTSourceBinding(ctx, r.Key, 3, 0)
	require.Error(t, err)
}

func TestMQTTReplayMarkerClearRoutesToOwnerAndRemovesDiscovery(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 256)
	s := nodes[1].store
	r := metadb.MQTTSourceBinding{Key: metadb.MQTTSourceBindingKey{Owner: metadb.MQTTBindingOwner{Kind: metadb.MQTTBindingChannel, ID: "2:group", Generation: "g"}, Namespace: "main", ClientID: "c", SessionGeneration: 1, SubscriptionGeneration: 2}, UID: "alice", Topic: "topic", Revision: 1, IntentRevision: 2, AuthorizationVersion: 1, OperationID: "subscribe", Stage: metadb.MQTTBindingPreparing, RecoveryAtMS: 1}
	_, err := s.ClearMQTTReplayMarker(ctx, metadb.MQTTBindingOwner{})
	require.Error(t, err)
	res, err := s.ClearMQTTReplayMarker(ctx, r.Key.Owner)
	require.NoError(t, err)
	require.Equal(t, metadb.MQTTSessionCASConflict, res.Status)
	_ = r
}
