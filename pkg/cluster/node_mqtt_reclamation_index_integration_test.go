//go:build integration

package cluster

import (
	"context"
	"fmt"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/stretchr/testify/require"
)

func TestMQTTReclamationIndexThreeNodePartialBuildRestart(t *testing.T) {
	voters := []ControlVoter{{NodeID: 1, Addr: freeTCPAddr(t)}, {NodeID: 2, Addr: freeTCPAddr(t)}, {NodeID: 3, Addr: freeTCPAddr(t)}}
	var nodes []*Node
	for _, v := range voters {
		cfg := Config{NodeID: v.NodeID, ListenAddr: v.Addr, DataDir: t.TempDir(), Control: ControlConfig{ClusterID: "mqtt-reclamation-index", Voters: voters, AllowBootstrap: true}, Slots: SlotConfig{InitialSlotCount: 2, HashSlotCount: 256, ReplicaCount: 3}}
		cfg.HealthReport.Interval = 200 * time.Millisecond
		n, e := New(cfg)
		require.NoError(t, e)
		nodes = append(nodes, n)
	}
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	key, e := slotproxy.MQTTSessionRoutingKey("main", "target")
	require.NoError(t, e)
	route := waitRouteKeyLeaderConverged(t, nodes, key)
	origin := firstNonLeaderNode(t, nodes, route.Leader)
	var sessions []metadb.MQTTSession
	for i := 0; i < 100000 && len(sessions) < 65; i++ {
		client := fmt.Sprintf("legacy-%06d", i)
		k, e := slotproxy.MQTTSessionRoutingKey("main", client)
		require.NoError(t, e)
		if origin.HashSlotForKey(k) != route.HashSlot {
			continue
		}
		s := metadb.MQTTSession{Namespace: "main", ClientID: client, UID: "alice", Generation: 1, Revision: 1, OwnerGeneration: 1, OwnerNodeID: origin.NodeID(), OwnerBootID: "boot", ConnectionID: 1, LeaseUntilMS: 9000, State: metadb.MQTTSessionActive, SessionExpirySec: 86400, ReceiveMaximum: 64, MaxPacketBytes: 1 << 20, NextPacketID: 1, NextDeliveryOrder: 1, QuotaMessages: 1000, QuotaBytes: 1 << 20, UpdatedAtMS: 1000}
		r, e := origin.CompareAndSwapMQTTSession(ctx, 0, s)
		require.NoError(t, e)
		require.Equal(t, metadb.MQTTSessionCASApplied, r.Status)
		s.Generation++
		s.Revision++
		s.OwnerGeneration++
		r, e = origin.CompareAndSwapMQTTSession(ctx, 1, s)
		require.NoError(t, e)
		require.Equal(t, metadb.MQTTSessionCASApplied, r.Status)
		sessions = append(sessions, s)
	}
	require.Len(t, sessions, 65)
	q := metadb.MQTTRead{Kind: metadb.MQTTReadSessionReclamation, Limit: 16}
	_, e = origin.ReadMQTTRecovery(ctx, route.HashSlot, q)
	require.Error(t, e)
	first, e := origin.BuildMQTTReclamationIndex(ctx, route.HashSlot)
	require.NoError(t, e)
	require.Equal(t, metadb.MQTTReclamationIndexResult{Scanned: 64}, first)
	_, e = origin.ReadMQTTRecovery(ctx, route.HashSlot, q)
	require.Error(t, e)
	stopNodes(t, nodes...)
	for i, n := range nodes {
		restarted, e := New(n.cfg)
		require.NoError(t, e)
		nodes[i] = restarted
	}
	startNodes(t, nodes...)
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])
	origin = nodes[0]
	last, e := origin.BuildMQTTReclamationIndex(ctx, route.HashSlot)
	require.NoError(t, e)
	require.Equal(t, metadb.MQTTReclamationIndexResult{Scanned: 1, Done: true}, last)
	for _, n := range nodes {
		scan := q
		seen := map[string]bool{}
		for range 5 {
			page, e := n.ReadMQTTRecovery(ctx, route.HashSlot, scan)
			require.NoError(t, e)
			for _, s := range page.Sessions {
				require.False(t, seen[s.ClientID])
				seen[s.ClientID] = true
				require.EqualValues(t, 2, s.Generation)
			}
			if page.Done {
				break
			}
			scan.After = page.After
		}
		require.Len(t, seen, 65)
	}
	s := sessions[0]
	clean, e := origin.ReclaimMQTTSession(ctx, metadb.MQTTSessionReclamation{Namespace: s.Namespace, ClientID: s.ClientID, ExpectedRevision: s.Revision, ThroughGeneration: 1, UpdatedAtMS: 2000})
	require.NoError(t, e)
	require.True(t, clean.Done)
	q.Limit = 64
	page, e := origin.ReadMQTTRecovery(ctx, route.HashSlot, q)
	require.NoError(t, e)
	require.True(t, page.Done)
	require.Len(t, page.Sessions, 64)
	retry, e := origin.BuildMQTTReclamationIndex(ctx, route.HashSlot)
	require.NoError(t, e)
	require.Equal(t, metadb.MQTTReclamationIndexResult{Done: true}, retry)
	t.Log("mqtt_reclamation_index_evidence: nodes=3 hash_slots=256 tcp=true disk=true coverage_pages=64,1 full_cluster_restart_between_pages=true incomplete_coverage_rejected=true authority_reads=3 cleanup_removes_candidate=true automatic_scheduler=false legacy_missing_index=storage_test_only")
}
