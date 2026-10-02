package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMQTTReclamationUsesSessionAuthorityAndCommittedCompletion(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 256)
	store := nodes[0].store
	ctx := context.Background()
	client := ""
	for i := 0; i < 10000; i++ {
		candidate := fmt.Sprintf("cleanup-%d", i)
		key, e := MQTTSessionRoutingKey("main", candidate)
		require.NoError(t, e)
		if store.cluster.SlotForKey(key) == 2 {
			client = candidate
			break
		}
	}
	require.NotEmpty(t, client)
	s := mqttProxySession(client)
	_, e := store.CompareAndSwapMQTTSession(ctx, 0, s)
	require.NoError(t, e)
	m := metadb.MQTTSessionReclamation{Namespace: s.Namespace, ClientID: s.ClientID, ExpectedRevision: 1, ThroughGeneration: 1, UpdatedAtMS: 2000}
	r, e := store.ReclaimMQTTSession(ctx, m)
	require.NoError(t, e)
	require.Equal(t, metadb.MQTTSessionCASConflict, r.Status)
	s.Revision++
	s.State = metadb.MQTTSessionEnded
	s.LeaseUntilMS = 0
	s.TerminationReason = metadb.MQTTSessionExpired
	_, e = store.CompareAndSwapMQTTSession(ctx, 1, s)
	require.NoError(t, e)
	m.ExpectedRevision = 2
	r, e = store.ReclaimMQTTSession(ctx, m)
	require.NoError(t, e)
	require.True(t, r.Done)
	require.EqualValues(t, 1, r.ReclaimedThroughGeneration)
	key, _ := MQTTSessionRoutingKey(s.Namespace, s.ClientID)
	_, found, e := nodes[0].db.ForHashSlot(store.cluster.HashSlotForKey(key)).GetMQTTSession(ctx, s.Namespace, s.ClientID)
	require.NoError(t, e)
	require.False(t, found)
	r, e = store.ReclaimMQTTSession(ctx, m)
	require.NoError(t, e)
	require.Equal(t, metadb.MQTTSessionCASUnchanged, r.Status)
	read, e := store.ReadMQTT(ctx, metadb.MQTTRead{Kind: metadb.MQTTReadSession, Namespace: s.Namespace, ClientID: s.ClientID})
	require.NoError(t, e)
	require.EqualValues(t, 1, read.Session.ReclaimedThroughGeneration)
}
func TestMQTTReclamationRejectsMissingAndContradictoryReceipts(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 256)
	ctx := context.Background()
	m := metadb.MQTTSessionReclamation{Namespace: "main", ClientID: "client", ExpectedRevision: 10, ThroughGeneration: 2, UpdatedAtMS: 2000}
	base := metadb.MQTTSessionReclamationResult{Status: metadb.MQTTSessionCASApplied, CurrentRevision: 11, ReclaimedThroughGeneration: 2, Done: true, RemovedSubscriptions: 1}
	store := &Store{cluster: nodes[0].cluster, db: nodes[0].db}
	for _, change := range []func(*metadb.MQTTSessionReclamationResult){func(r *metadb.MQTTSessionReclamationResult) { r.Status = 0 }, func(r *metadb.MQTTSessionReclamationResult) { r.CurrentRevision = 12 }, func(r *metadb.MQTTSessionReclamationResult) { r.ReclaimedThroughGeneration = 1 }, func(r *metadb.MQTTSessionReclamationResult) { r.ReclaimedThroughGeneration = 3 }, func(r *metadb.MQTTSessionReclamationResult) { r.Done = false }, func(r *metadb.MQTTSessionReclamationResult) { r.RemovedSubscriptions = 65 }, func(r *metadb.MQTTSessionReclamationResult) { r.RemovedSubscriptions = -1 }, func(r *metadb.MQTTSessionReclamationResult) { r.Status = metadb.MQTTSessionCASUnchanged }, func(r *metadb.MQTTSessionReclamationResult) { r.Status = metadb.MQTTSessionCASConflict }} {
		bad := base
		change(&bad)
		body, e := json.Marshal(bad)
		require.NoError(t, e)
		store.cluster = &mqttResultCluster{Cluster: nodes[0].cluster, body: body}
		_, e = store.ReclaimMQTTSession(ctx, m)
		require.ErrorIs(t, e, metadb.ErrCorruptValue)
	}
	for _, body := range []string{"", "null", "{}", `{"Status":1,"CurrentRevision":11,"Done":true}`, `{"Status":1,"CurrentRevision":11,"ReclaimedThroughGeneration":2,"Done":true,"future":1}`} {
		store.cluster = &mqttResultCluster{Cluster: nodes[0].cluster, body: []byte(body)}
		_, e := store.ReclaimMQTTSession(ctx, m)
		require.Error(t, e)
	}
	noResult := &mqttNoResultCluster{Cluster: nodes[0].cluster}
	store.cluster = noResult
	_, e := store.ReclaimMQTTSession(ctx, m)
	require.Error(t, e)
	require.Zero(t, noResult.writes)
	// Completion may already precede this observation's revision, including after a new lifecycle.
	base.Status = metadb.MQTTSessionCASUnchanged
	base.CurrentRevision = 30
	base.ReclaimedThroughGeneration = 3
	base.RemovedSubscriptions = 0
	body, e := json.Marshal(base)
	require.NoError(t, e)
	store.cluster = &mqttResultCluster{Cluster: nodes[0].cluster, body: body}
	r, e := store.ReclaimMQTTSession(ctx, m)
	require.NoError(t, e)
	require.True(t, r.Done)
}
