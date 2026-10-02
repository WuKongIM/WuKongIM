package proxy

import (
	"context"
	"encoding/json"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestMQTTSourceOwnersFreshRoutingAndClosedReply(t *testing.T) {
	testMQTTSourceDiscovery(t, metadb.MQTTReadSourceOwners, false)
}

func TestMQTTReplaySourcesFreshRoutingAndClosedReply(t *testing.T) {
	require.Equal(t, metadb.MQTTReadKind(17), metadb.MQTTReadReplaySources)
	testMQTTSourceDiscovery(t, metadb.MQTTReadReplaySources, true)
}

func testMQTTSourceDiscovery(t *testing.T, kind metadb.MQTTReadKind, removed bool) {
	t.Helper()
	nodes := startTwoNodeHashSlotStores(t, 256)
	s := nodes[0].store
	ctx := context.Background()
	owner := metadb.MQTTBindingOwner{Kind: metadb.MQTTBindingChannel, ID: "2:group", Generation: "g"}
	for _, client := range []string{"a", "b"} {
		r := metadb.MQTTSourceBinding{Key: metadb.MQTTSourceBindingKey{Owner: owner, Namespace: "main", ClientID: client, SessionGeneration: 1, SubscriptionGeneration: 2}, UID: "alice", Topic: "topic", Revision: 1, IntentRevision: 2, AuthorizationVersion: 1, OperationID: "subscribe", Stage: metadb.MQTTBindingPreparing, RecoveryAtMS: 1000, UpdatedAtMS: 1000}
		if removed {
			r.Stage, r.ProgressRevision, r.ReleaseReason, r.RecoveryAtMS = metadb.MQTTBindingRemoved, 2, metadb.MQTTBindingSessionEnded, 0
		}
		res, err := s.CompareAndSwapMQTTSourceBinding(ctx, 0, r)
		require.NoError(t, err)
		require.Equal(t, metadb.MQTTSessionCASApplied, res.Status)
	}
	hs := nodes[0].cluster.HashSlotForKey("group")
	query := metadb.MQTTRead{Kind: kind, Limit: 1}
	require.True(t, query.Recovery())
	if !removed {
		require.Equal(t, metadb.MQTTReadKind(16), query.Kind)
	}
	for _, n := range nodes {
		p, err := n.store.ReadMQTTRecovery(ctx, hs, query)
		require.NoError(t, err)
		require.Equal(t, []metadb.MQTTBindingOwner{owner}, p.SourceOwners)
		require.Equal(t, owner, p.After.SourceOwner)
		require.True(t, p.Done)
	}
	_, err := s.ReadMQTT(ctx, query)
	require.Error(t, err, "source owners require a hash-Slot scan")
	q := mqttReadRPC{Format: 1, SlotID: 1, HashSlot: hs, Query: query}
	result := metadb.MQTTReadResult{SourceOwners: []metadb.MQTTBindingOwner{owner}, After: metadb.MQTTReadCursor{SourceOwner: owner}, Done: true}
	for _, mode := range []string{"good", "wrong_cursor", "extra_rows", "wrong_kind", "duplicate", "out_of_order", "regressed", "empty_advanced", "bad_owner", "uid", "short_unfinished"} {
		t.Run(mode, func(t *testing.T) {
			r := result
			r.SourceOwners = append([]metadb.MQTTBindingOwner(nil), result.SourceOwners...)
			request := q
			switch mode {
			case "wrong_cursor":
				r.After.SourceOwner.Generation = "other"
			case "extra_rows":
				r.Bindings = []metadb.MQTTSourceBinding{{}}
			case "wrong_kind":
				request.Query.Kind = metadb.MQTTReadSourceRecovery
			case "duplicate":
				request.Query.Limit = 2
				r.SourceOwners = append(r.SourceOwners, owner)
			case "out_of_order":
				request.Query.Limit = 2
				later := owner
				later.ID = "2:longer"
				r.SourceOwners = []metadb.MQTTBindingOwner{later, owner}
			case "regressed":
				request.Query.After.SourceOwner = owner
			case "empty_advanced":
				r.SourceOwners = nil
			case "bad_owner":
				r.SourceOwners[0].Generation = ""
				r.After.SourceOwner = r.SourceOwners[0]
			case "uid":
				r.SourceOwners[0] = metadb.MQTTBindingOwner{Kind: metadb.MQTTBindingUID, ID: "alice"}
				r.After.SourceOwner = r.SourceOwners[0]
			case "short_unfinished":
				request.Query.Limit = 2
				r.Done = false
			}
			reply := mqttReadReply{Format: 1, SlotID: request.SlotID, HashSlot: request.HashSlot, Query: request.Query, Status: rpcStatusOK, Result: &r}
			body, err := json.Marshal(reply)
			require.NoError(t, err)
			_, err = decodeMQTTReadReply(body, request)
			if mode == "good" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	changing := &changingReadAuthority{proxyTestCluster: nodes[1].cluster}
	stale := NewChannelMetadataStore(changing, nodes[1].db)
	_, err = stale.ReadMQTTRecovery(ctx, changing.HashSlotsOf(2)[0], query)
	require.ErrorIs(t, err, ErrReadStaleRoute)
}

func TestMQTTSourceOwnersDoesNotAlterOlderReadJSON(t *testing.T) {
	for _, v := range []any{metadb.MQTTRead{Kind: metadb.MQTTReadSession, Namespace: "main", ClientID: "client"}, metadb.MQTTReadResult{Done: true}} {
		b, err := json.Marshal(v)
		require.NoError(t, err)
		require.NotContains(t, string(b), "source_owner")
	}
}
