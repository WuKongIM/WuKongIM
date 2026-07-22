package cluster

import (
	"context"
	"strconv"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	clusterrouting "github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	benchmarkConversationAuthorityGroups   []conversationAuthorityActiveBatchGroup
	benchmarkConversationAuthorityFailures []conversationAuthorityActiveBatchFailure
)

// benchmarkConversationAuthorityNode models the root Node's legacy full-route
// conversion so the benchmark includes both placement-slice clone boundaries.
type benchmarkConversationAuthorityNode struct {
	router *clusterrouting.Router
}

type benchmarkLightweightConversationAuthorityNode struct {
	*benchmarkConversationAuthorityNode
}

func (n *benchmarkConversationAuthorityNode) NodeID() uint64 {
	return 1
}

func (n *benchmarkConversationAuthorityNode) RouteKey(string) (clusterpkg.Route, error) {
	panic("RouteKey must not be called by the bulk grouping benchmark")
}

func (n *benchmarkConversationAuthorityNode) RouteKeysPartial(uids []string) ([]clusterpkg.RouteKeyResult, error) {
	routes, err := n.router.RouteKeysPartial(uids)
	if err != nil {
		return nil, err
	}
	results := make([]clusterpkg.RouteKeyResult, len(routes))
	for index, result := range routes {
		results[index].Err = result.Err
		results[index].Route = clusterpkg.Route{
			HashSlot:        result.Route.HashSlot,
			SlotID:          result.Route.SlotID,
			Leader:          result.Route.Leader,
			LeaderTerm:      result.Route.LeaderTerm,
			ConfigEpoch:     result.Route.ConfigEpoch,
			PreferredLeader: result.Route.PreferredLeader,
			Peers:           append([]uint64(nil), result.Route.Peers...),
			Revision:        result.Route.Revision,
		}
	}
	return results, nil
}

func (n *benchmarkLightweightConversationAuthorityNode) RouteAuthoritiesPartial(uids []string) ([]clusterpkg.RouteAuthorityResult, error) {
	return n.router.RouteAuthoritiesPartial(uids)
}

func (n *benchmarkConversationAuthorityNode) CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error) {
	panic("CallRPC must not be called by the grouping benchmark")
}

func (n *benchmarkConversationAuthorityNode) RegisterRPC(uint8, clusterpkg.NodeRPCHandler) {
	panic("RegisterRPC must not be called by the grouping benchmark")
}

func (n *benchmarkConversationAuthorityNode) WatchRouteAuthorities() <-chan clusterpkg.RouteAuthorityEvent {
	panic("WatchRouteAuthorities must not be called by the grouping benchmark")
}

func BenchmarkConversationAuthorityGroupActiveBatchesByTargetPartial(b *testing.B) {
	const (
		recipientCount = 512
		hashSlotCount  = 256
		logicalSlots   = 10
		leaderCount    = 3
	)

	router := newBenchmarkConversationAuthorityRouter(b, hashSlotCount, logicalSlots, leaderCount)
	recipientUIDs := benchmarkConversationAuthorityUIDsByHashSlot(b, hashSlotCount, recipientCount/hashSlotCount)
	recipients := make([]conversationactive.ActiveEntry, len(recipientUIDs))
	for index, uid := range recipientUIDs {
		recipients[index] = conversationactive.ActiveEntry{UID: uid}
	}
	batches := []conversationactive.ActiveBatch{{
		Kind:        metadb.ConversationKindNormal,
		SenderUID:   "sender",
		ChannelID:   "benchmark-channel",
		ChannelType: 2,
		MessageSeq:  1,
		ActiveAtMS:  1,
		Recipients:  recipients,
	}}
	legacy := &benchmarkConversationAuthorityNode{router: router}
	cases := []struct {
		name string
		node ConversationAuthorityNode
	}{
		{name: "legacy_route_keys_partial", node: legacy},
		{name: "lightweight_authorities_partial", node: &benchmarkLightweightConversationAuthorityNode{benchmarkConversationAuthorityNode: legacy}},
	}
	for _, benchCase := range cases {
		b.Run(benchCase.name+"_unique_512_recipients_256_targets", func(b *testing.B) {
			client := &ConversationAuthorityClient{node: benchCase.node}
			groups, failures, err := client.groupActiveBatchesByTargetPartial(batches)
			if err != nil {
				b.Fatalf("groupActiveBatchesByTargetPartial() setup error = %v", err)
			}
			assertConversationAuthorityBenchmarkShape(b, groups, failures, recipientCount, hashSlotCount, logicalSlots, leaderCount)

			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				groups, failures, err = client.groupActiveBatchesByTargetPartial(batches)
				if err != nil {
					b.Fatalf("groupActiveBatchesByTargetPartial() error = %v", err)
				}
				benchmarkConversationAuthorityGroups = groups
				benchmarkConversationAuthorityFailures = failures
			}
			b.StopTimer()

			assertConversationAuthorityBenchmarkShape(b, benchmarkConversationAuthorityGroups, benchmarkConversationAuthorityFailures, recipientCount, hashSlotCount, logicalSlots, leaderCount)
		})
	}
}

func newBenchmarkConversationAuthorityRouter(b *testing.B, hashSlotCount, logicalSlotCount, leaderCount int) *clusterrouting.Router {
	b.Helper()
	nodes := make([]control.Node, leaderCount)
	for index := range nodes {
		nodes[index] = control.Node{
			NodeID: uint64(index + 1),
			Addr:   "127.0.0.1:" + strconv.Itoa(10001+index),
			Roles:  []control.Role{control.RoleData},
			Status: control.NodeAlive,
		}
	}
	peers := make([]uint64, leaderCount)
	for index := range peers {
		peers[index] = uint64(index + 1)
	}
	slots := make([]control.SlotAssignment, logicalSlotCount)
	statuses := make([]clusterrouting.SlotStatus, logicalSlotCount)
	for index := range slots {
		slotID := uint32(index + 1)
		leader := uint64(index%leaderCount + 1)
		slots[index] = control.SlotAssignment{
			SlotID:          slotID,
			DesiredPeers:    append([]uint64(nil), peers...),
			ConfigEpoch:     9,
			PreferredLeader: leader,
		}
		statuses[index] = clusterrouting.SlotStatus{SlotID: slotID, Leader: leader, LeaderTerm: 7}
	}
	ranges := make([]control.HashSlotRange, hashSlotCount)
	for hashSlot := range ranges {
		ranges[hashSlot] = control.HashSlotRange{
			From:   uint16(hashSlot),
			To:     uint16(hashSlot),
			SlotID: uint32(hashSlot%logicalSlotCount + 1),
		}
	}
	router := clusterrouting.NewRouter()
	if err := router.UpdateControlSnapshot(control.Snapshot{
		Revision:     11,
		ControllerID: 1,
		Nodes:        nodes,
		Slots:        slots,
		HashSlots: control.HashSlotTable{
			Revision: 11,
			Count:    uint16(hashSlotCount),
			Ranges:   ranges,
		},
	}); err != nil {
		b.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	router.UpdateSlotLeaders(statuses)
	return router
}

func benchmarkConversationAuthorityUIDsByHashSlot(b *testing.B, hashSlotCount, perHashSlot int) []string {
	b.Helper()
	want := hashSlotCount * perHashSlot
	uids := make([]string, 0, want)
	counts := make([]int, hashSlotCount)
	for candidate := 0; len(uids) < want; candidate++ {
		uid := "recipient-" + strconv.Itoa(candidate)
		hashSlot := int(clusterrouting.HashSlotForKey(uid, uint16(hashSlotCount)))
		if counts[hashSlot] >= perHashSlot {
			continue
		}
		counts[hashSlot]++
		uids = append(uids, uid)
		if candidate > 1_000_000 {
			b.Fatalf("unable to generate balanced benchmark UIDs")
		}
	}
	return uids
}

func assertConversationAuthorityBenchmarkShape(
	b *testing.B,
	groups []conversationAuthorityActiveBatchGroup,
	failures []conversationAuthorityActiveBatchFailure,
	recipientCount int,
	hashSlotCount int,
	logicalSlotCount int,
	leaderCount int,
) {
	b.Helper()
	if len(failures) != 0 {
		b.Fatalf("route failures = %d, want 0", len(failures))
	}
	if len(groups) != hashSlotCount {
		b.Fatalf("exact target groups = %d, want %d", len(groups), hashSlotCount)
	}

	hashSlots := make(map[uint16]struct{}, hashSlotCount)
	logicalSlots := make(map[uint32]struct{}, logicalSlotCount)
	leaders := make(map[uint64]struct{}, leaderCount)
	recipientRows := 0
	senderGroups := 0
	for _, group := range groups {
		hashSlots[group.target.HashSlot] = struct{}{}
		logicalSlots[group.target.SlotID] = struct{}{}
		leaders[group.target.LeaderNodeID] = struct{}{}
		recipientRows += len(group.batch.Recipients)
		if group.batch.SenderUID != "" {
			senderGroups++
		}
	}
	if len(hashSlots) != hashSlotCount {
		b.Fatalf("hash slots = %d, want %d", len(hashSlots), hashSlotCount)
	}
	if len(logicalSlots) != logicalSlotCount {
		b.Fatalf("logical slots = %d, want %d", len(logicalSlots), logicalSlotCount)
	}
	if len(leaders) != leaderCount {
		b.Fatalf("leaders = %d, want %d", len(leaders), leaderCount)
	}
	if recipientRows != recipientCount {
		b.Fatalf("recipient rows = %d, want %d", recipientRows, recipientCount)
	}
	if senderGroups != 1 {
		b.Fatalf("sender groups = %d, want 1", senderGroups)
	}
}
