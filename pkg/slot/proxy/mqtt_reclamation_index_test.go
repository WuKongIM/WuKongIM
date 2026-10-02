package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	db "github.com/WuKongIM/WuKongIM/pkg/db"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMQTTReclamationIndexRoutedCoverageAndFreshDiscovery(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 256)
	store := nodes[0].store
	ctx := context.Background()
	var key, client string
	for i := 0; i < 10000; i++ {
		client = fmt.Sprintf("index-%d", i)
		key, _ = MQTTSessionRoutingKey("main", client)
		if store.cluster.SlotForKey(key) == 2 {
			break
		}
	}
	s := mqttProxySession(client)
	_, e := store.CompareAndSwapMQTTSession(ctx, 0, s)
	require.NoError(t, e)
	s.Revision++
	s.Generation++
	s.OwnerGeneration++
	_, e = store.CompareAndSwapMQTTSession(ctx, 1, s)
	require.NoError(t, e)
	hs := store.cluster.HashSlotForKey(key)
	q := metadb.MQTTRead{Kind: metadb.MQTTReadSessionReclamation, Limit: 1}
	_, e = store.ReadMQTTRecovery(ctx, hs, q)
	require.Error(t, e)
	build, e := store.BuildMQTTReclamationIndex(ctx, hs)
	require.NoError(t, e)
	require.Equal(t, metadb.MQTTReclamationIndexResult{Scanned: 1, Done: true}, build)
	// A node-local empty replica has no coverage and cannot answer this query.
	_, e = nodes[0].db.ReadMQTTState(ctx, hs, q)
	require.ErrorIs(t, e, db.ErrConflict)
	for range 2 {
		before := nodes[1].cluster.nextIndex[2]
		r, e := store.ReadMQTTRecovery(ctx, hs, q)
		require.NoError(t, e)
		require.Equal(t, []metadb.MQTTSession{s}, r.Sessions)
		require.Equal(t, before+1, nodes[1].cluster.nextIndex[2])
	}
	_, e = store.ReadMQTT(ctx, q)
	require.Error(t, e)
	_, e = store.BuildMQTTReclamationIndex(ctx, 256)
	require.Error(t, e)
}
func TestMQTTReclamationIndexReplyValidation(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 256)
	store := &Store{cluster: nodes[0].cluster, db: nodes[0].db}
	ctx := context.Background()
	for _, body := range []string{"", "null", "{}", `{"Scanned":-1,"Done":true}`, `{"Scanned":65,"Done":true}`, `{"Scanned":63,"Done":false}`, `{"Scanned":64,"Done":false,"future":1}`} {
		store.cluster = &mqttResultCluster{Cluster: nodes[0].cluster, body: []byte(body)}
		_, e := store.BuildMQTTReclamationIndex(ctx, 1)
		require.Error(t, e, body)
	}
	noResult := &mqttNoResultCluster{Cluster: nodes[0].cluster}
	store.cluster = noResult
	_, e := store.BuildMQTTReclamationIndex(ctx, 1)
	require.Error(t, e)
	require.Zero(t, noResult.writes)
	for _, mode := range []string{"valid", "live", "duplicate", "backward", "missing_cursor", "foreign_cursor", "mixed"} {
		t.Run(mode, func(t *testing.T) {
			a := mqttProxySession("a")
			a.Generation = 2
			b := a
			b.ClientID = "b"
			q := metadb.MQTTRead{Kind: metadb.MQTTReadSessionReclamation, Limit: 2}
			r := metadb.MQTTReadResult{Sessions: []metadb.MQTTSession{a, b}, After: metadb.MQTTReadCursor{Session: metadb.MQTTSessionCursor{Namespace: "main", ClientID: "b"}}, Done: true}
			switch mode {
			case "live":
				r.Sessions[0].Generation = 1
			case "duplicate":
				r.Sessions[1] = a
			case "backward":
				r.Sessions[0], r.Sessions[1] = b, a
			case "missing_cursor":
				r.After = metadb.MQTTReadCursor{}
			case "foreign_cursor":
				r.After.Session.ClientID = "x"
			case "mixed":
				r.After.Topic = "x"
			}
			req := mqttReadRPC{Format: 1, SlotID: 2, HashSlot: 7, Query: q}
			body, e := json.Marshal(mqttReadReply{Format: 1, SlotID: 2, HashSlot: 7, Query: q, Status: rpcStatusOK, Result: &r})
			require.NoError(t, e)
			_, e = decodeMQTTReadReply(body, req)
			if mode == "valid" {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
			}
		})
	}
}
