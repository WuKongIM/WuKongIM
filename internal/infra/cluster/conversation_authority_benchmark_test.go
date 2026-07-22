package cluster

import (
	"context"
	"strconv"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	benchmarkConversationAuthorityGroups   []conversationAuthorityActiveBatchGroup
	benchmarkConversationAuthorityFailures []conversationAuthorityActiveBatchFailure
)

// benchmarkConversationAuthorityNode returns precomputed aligned routes without
// adding the recording and input-copy allocations used by correctness fakes.
type benchmarkConversationAuthorityNode struct {
	routes []clusterpkg.RouteKeyResult
}

func (n *benchmarkConversationAuthorityNode) NodeID() uint64 {
	return 1
}

func (n *benchmarkConversationAuthorityNode) RouteKey(string) (clusterpkg.Route, error) {
	panic("RouteKey must not be called by the bulk grouping benchmark")
}

func (n *benchmarkConversationAuthorityNode) RouteKeysPartial(uids []string) ([]clusterpkg.RouteKeyResult, error) {
	if len(uids) != len(n.routes) {
		panic("unexpected aligned UID count")
	}
	return n.routes, nil
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
	b.Run("unique_512_recipients_256_targets", func(b *testing.B) {
		const (
			recipientCount = 512
			hashSlotCount  = 256
			logicalSlots   = 10
			leaderCount    = 3
		)

		recipients := make([]conversationactive.ActiveEntry, 0, recipientCount)
		routes := make([]clusterpkg.RouteKeyResult, recipientCount+1)
		routes[0].Route = benchmarkConversationAuthorityRoute(0, logicalSlots, leaderCount)
		for index := 0; index < recipientCount; index++ {
			recipients = append(recipients, conversationactive.ActiveEntry{UID: "recipient-" + strconv.Itoa(index)})
			routes[index+1].Route = benchmarkConversationAuthorityRoute(index%hashSlotCount, logicalSlots, leaderCount)
		}

		client := &ConversationAuthorityClient{
			node: &benchmarkConversationAuthorityNode{routes: routes},
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

func benchmarkConversationAuthorityRoute(hashSlot, logicalSlots, leaderCount int) clusterpkg.Route {
	logicalSlot := hashSlot % logicalSlots
	return clusterpkg.Route{
		HashSlot:       uint16(hashSlot),
		SlotID:         uint32(logicalSlot),
		Leader:         uint64(logicalSlot%leaderCount + 1),
		LeaderTerm:     7,
		ConfigEpoch:    9,
		Revision:       11,
		AuthorityEpoch: 13,
	}
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
