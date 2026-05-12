package deliverytag

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTagVersionIncrementsWithoutChangingKey(t *testing.T) {
	now := time.Unix(100, 0)
	keys := newSequenceKeyGen("leader-a")
	manager := NewManager(Options{LocalNodeID: 1, TTL: time.Minute, Now: func() time.Time { return now }, NewTagKey: keys.Next})
	topology := testTopology(10, 1, 2)

	initial, created := manager.BuildLeaderTag(BuildRequest{
		ChannelKey:                "group:alpha",
		SubscriberMutationVersion: 1,
		Topology:                  topology,
		Partitions: []NodePartition{
			{NodeID: 1, UIDs: []string{"u1", "u3"}},
			{NodeID: 2, UIDs: []string{"u2"}},
		},
	})
	require.True(t, created)
	require.Equal(t, "leader-a-1", initial.Key)
	require.Equal(t, uint64(1), initial.TagVersion)

	updated, created := manager.BuildLeaderTag(BuildRequest{
		ChannelKey:                "group:alpha",
		SubscriberMutationVersion: 2,
		Topology:                  topology,
		Partitions: []NodePartition{
			{NodeID: 1, UIDs: []string{"u1"}},
			{NodeID: 2, UIDs: []string{"u2", "u4"}},
		},
	})
	require.True(t, created)
	require.Equal(t, initial.Key, updated.Key)
	require.Equal(t, uint64(2), updated.TagVersion)
	require.Equal(t, uint64(2), updated.SubscriberMutationVersion)
	require.Equal(t, []string{"u1"}, updated.PartitionForNode(1).UIDs)
	require.Equal(t, []string{"u2", "u4"}, updated.PartitionForNode(2).UIDs)
}

func TestFollowerCachesOnlyLocalPartitionTag(t *testing.T) {
	manager := NewManager(Options{LocalNodeID: 2, TTL: time.Minute, Now: time.Now, NewTagKey: newSequenceKeyGen("unused").Next})
	leaderTag := DeliveryTag{
		Key:                       "tag-1",
		ChannelKey:                "group:beta",
		TagVersion:                4,
		SubscriberMutationVersion: 7,
		Topology:                  testTopology(10, 1, 2),
		Partitions: []NodePartition{
			{NodeID: 1, UIDs: []string{"u1", "u3"}},
			{NodeID: 2, UIDs: []string{"u2"}},
			{NodeID: 3, UIDs: []string{"u4"}},
		},
	}

	stored, ok := manager.StoreFollowerPartition(leaderTag)
	require.True(t, ok)
	require.Equal(t, []NodePartition{{NodeID: 2, UIDs: []string{"u2"}}}, stored.Partitions)

	cached, ok := manager.LookupTag("tag-1")
	require.True(t, ok)
	require.Equal(t, []NodePartition{{NodeID: 2, UIDs: []string{"u2"}}}, cached.Partitions)
	require.Empty(t, cached.PartitionForNode(1).UIDs)
}

func TestStaleRequestDoesNotEvictNewerLocalTag(t *testing.T) {
	manager := NewManager(Options{LocalNodeID: 2, TTL: time.Minute, Now: time.Now, NewTagKey: newSequenceKeyGen("unused").Next})
	current := DeliveryTag{
		Key:                       "tag-1",
		ChannelKey:                "group:gamma",
		TagVersion:                5,
		SubscriberMutationVersion: 9,
		Topology:                  testTopology(10, 1, 2),
		Partitions:                []NodePartition{{NodeID: 2, UIDs: []string{"new"}}},
	}
	require.True(t, mustStoreFollower(t, manager, current))

	stale := TagRef{ChannelKey: "group:gamma", TagKey: "tag-1", TagVersion: 4, SubscriberMutationVersion: 8, Topology: testTopology(10, 1, 2)}
	cached, hit, reason := manager.LookupLocalPartition(stale)
	require.False(t, hit)
	require.Equal(t, LookupStaleRequest, reason)
	require.Equal(t, []string{"new"}, cached.PartitionForNode(2).UIDs)

	cached, ok := manager.LookupTag("tag-1")
	require.True(t, ok)
	require.Equal(t, uint64(5), cached.TagVersion)
	require.Equal(t, []string{"new"}, cached.PartitionForNode(2).UIDs)
}

func TestStaleDifferentTagKeyDoesNotEvictNewerLocalTag(t *testing.T) {
	manager := NewManager(Options{LocalNodeID: 2, TTL: time.Minute, Now: time.Now, NewTagKey: newSequenceKeyGen("unused").Next})
	fresh := DeliveryTag{
		Key:                       "leader-b-1",
		ChannelKey:                "group:fresh",
		TagVersion:                1,
		SubscriberMutationVersion: 9,
		Topology:                  testTopology(10, 1, 2),
		Partitions:                []NodePartition{{NodeID: 2, UIDs: []string{"new"}}},
	}
	require.True(t, mustStoreFollower(t, manager, fresh))

	oldDelayedResponse := DeliveryTag{
		Key:                       "leader-a-8",
		ChannelKey:                "group:fresh",
		TagVersion:                8,
		SubscriberMutationVersion: 8,
		Topology:                  testTopology(10, 1, 2),
		Partitions:                []NodePartition{{NodeID: 2, UIDs: []string{"old"}}},
	}
	stored, ok := manager.StoreFollowerPartition(oldDelayedResponse)
	require.False(t, ok)
	require.Equal(t, fresh.Key, stored.Key)
	require.Equal(t, []string{"new"}, stored.PartitionForNode(2).UIDs)

	current, ok := manager.CurrentRef("group:fresh")
	require.True(t, ok)
	require.Equal(t, fresh.Key, current.TagKey)
	require.Equal(t, uint64(9), current.SubscriberMutationVersion)
}

func TestLookupLocalPartitionRejectsSubscriberAndSourceFenceMismatch(t *testing.T) {
	manager := NewManager(Options{LocalNodeID: 2, TTL: time.Minute, Now: time.Now, NewTagKey: newSequenceKeyGen("unused").Next})
	current := DeliveryTag{
		Key:                             "tag-source",
		ChannelKey:                      "cmd:group:source",
		TagVersion:                      3,
		SubscriberMutationVersion:       5,
		SourceChannelKey:                "group:source",
		SourceSubscriberMutationVersion: 11,
		Topology:                        testTopology(10, 1, 2),
		Partitions:                      []NodePartition{{NodeID: 2, UIDs: []string{"new"}}},
	}
	require.True(t, mustStoreFollower(t, manager, current))

	_, hit, reason := manager.LookupLocalPartition(TagRef{
		ChannelKey:                      current.ChannelKey,
		TagKey:                          current.Key,
		TagVersion:                      current.TagVersion,
		SubscriberMutationVersion:       current.SubscriberMutationVersion - 1,
		SourceChannelKey:                current.SourceChannelKey,
		SourceSubscriberMutationVersion: current.SourceSubscriberMutationVersion,
		Topology:                        current.Topology,
	})
	require.False(t, hit)
	require.Equal(t, LookupStaleRequest, reason)

	_, hit, reason = manager.LookupLocalPartition(TagRef{
		ChannelKey:                      current.ChannelKey,
		TagKey:                          current.Key,
		TagVersion:                      current.TagVersion,
		SubscriberMutationVersion:       current.SubscriberMutationVersion,
		SourceChannelKey:                current.SourceChannelKey,
		SourceSubscriberMutationVersion: current.SourceSubscriberMutationVersion - 1,
		Topology:                        current.Topology,
	})
	require.False(t, hit)
	require.Equal(t, LookupStaleRequest, reason)
}

func TestLookupLocalPartitionRefReturnsSharedTagWithoutAllocating(t *testing.T) {
	manager := NewManager(Options{LocalNodeID: 2, TTL: time.Minute, Now: time.Now, NewTagKey: newSequenceKeyGen("unused").Next})
	current := DeliveryTag{
		Key:                       "tag-ref",
		ChannelKey:                "group:ref",
		TagVersion:                5,
		SubscriberMutationVersion: 9,
		Topology:                  testTopology(10, 1, 2),
		Partitions:                []NodePartition{{NodeID: 2, UIDs: []string{"u1", "u2"}}},
	}
	require.True(t, mustStoreFollower(t, manager, current))

	ref := TagRef{
		ChannelKey:                current.ChannelKey,
		TagKey:                    current.Key,
		TagVersion:                current.TagVersion,
		SubscriberMutationVersion: current.SubscriberMutationVersion,
		Topology:                  current.Topology,
	}

	var tag DeliveryTag
	var hit bool
	var reason LookupReason
	allocs := testing.AllocsPerRun(100, func() {
		tag, hit, reason = manager.LookupLocalPartitionRef(ref)
	})
	require.Zero(t, allocs)
	require.True(t, hit)
	require.Equal(t, LookupHit, reason)
	require.Equal(t, []string{"u1", "u2"}, tag.Partitions[0].UIDs)
}

func TestPartitionTopologyMismatchForcesRefetch(t *testing.T) {
	manager := NewManager(Options{LocalNodeID: 2, TTL: time.Minute, Now: time.Now, NewTagKey: newSequenceKeyGen("unused").Next})
	storedTopology := testTopology(10, 1, 2)
	require.True(t, mustStoreFollower(t, manager, DeliveryTag{
		Key:                       "tag-topology",
		ChannelKey:                "group:delta",
		TagVersion:                3,
		SubscriberMutationVersion: 6,
		Topology:                  storedTopology,
		Partitions:                []NodePartition{{NodeID: 2, UIDs: []string{"u2"}}},
	}))

	changedTopology := testTopology(11, 1, 2)
	_, hit, reason := manager.LookupLocalPartition(TagRef{
		ChannelKey:                "group:delta",
		TagKey:                    "tag-topology",
		TagVersion:                3,
		SubscriberMutationVersion: 6,
		Topology:                  changedTopology,
	})
	require.False(t, hit)
	require.Equal(t, LookupTopologyMismatch, reason)

	changedAuthority := testTopology(10, 1, 2)
	changedAuthority.SlotAuthorityRefs[1].BalanceVersion++
	_, hit, reason = manager.LookupLocalPartition(TagRef{
		ChannelKey:                "group:delta",
		TagKey:                    "tag-topology",
		TagVersion:                3,
		SubscriberMutationVersion: 6,
		Topology:                  changedAuthority,
	})
	require.False(t, hit)
	require.Equal(t, LookupTopologyMismatch, reason)
}

func TestTTLCleanupRemovesColdTagsAndStaleChannelRefs(t *testing.T) {
	now := time.Unix(200, 0)
	manager := NewManager(Options{LocalNodeID: 1, TTL: time.Second, Now: func() time.Time { return now }, NewTagKey: newSequenceKeyGen("leader").Next})
	tag, _ := manager.BuildLeaderTag(BuildRequest{
		ChannelKey:                "group:ttl",
		SubscriberMutationVersion: 1,
		Topology:                  testTopology(1, 1),
		Partitions:                []NodePartition{{NodeID: 1, UIDs: []string{"u1"}}},
	})
	require.NotEmpty(t, tag.Key)

	now = now.Add(2 * time.Second)
	removed := manager.CleanupExpired()
	require.Equal(t, 1, removed)
	_, ok := manager.LookupTag(tag.Key)
	require.False(t, ok)
	_, ok = manager.CurrentRef("group:ttl")
	require.False(t, ok)
}

func TestDerivedTagStaleWhenSourceSubscriberMutationVersionAdvances(t *testing.T) {
	manager := NewManager(Options{LocalNodeID: 1, TTL: time.Minute, Now: time.Now, NewTagKey: newSequenceKeyGen("leader").Next})
	tag, _ := manager.BuildLeaderTag(BuildRequest{
		ChannelKey:                      "cmd:group:omega",
		SubscriberMutationVersion:       1,
		SourceChannelKey:                "group:omega",
		SourceSubscriberMutationVersion: 10,
		Topology:                        testTopology(1, 1),
		Partitions:                      []NodePartition{{NodeID: 1, UIDs: []string{"u1"}}},
	})
	require.Equal(t, uint64(10), tag.SourceSubscriberMutationVersion)

	require.False(t, manager.IsSourceStale("cmd:group:omega", SourceVersion{ChannelKey: "group:omega", SubscriberMutationVersion: 10}))
	require.True(t, manager.IsSourceStale("cmd:group:omega", SourceVersion{ChannelKey: "group:omega", SubscriberMutationVersion: 11}))
}

func TestLeaderIncarnationCanMintFreshTagKey(t *testing.T) {
	manager := NewManager(Options{LocalNodeID: 2, TTL: time.Minute, Now: time.Now, NewTagKey: newSequenceKeyGen("leader-b").Next})
	oldFollowerTag := DeliveryTag{
		Key:                       "leader-a-1",
		ChannelKey:                "group:zeta",
		TagVersion:                8,
		SubscriberMutationVersion: 8,
		Topology:                  testTopology(1, 2),
		Partitions:                []NodePartition{{NodeID: 2, UIDs: []string{"old"}}},
	}
	require.True(t, mustStoreFollower(t, manager, oldFollowerTag))

	fresh, created := manager.BuildLeaderTag(BuildRequest{
		ChannelKey:                "group:zeta",
		SubscriberMutationVersion: 9,
		Topology:                  testTopology(1, 2),
		Partitions:                []NodePartition{{NodeID: 2, UIDs: []string{"new"}}},
		MintFreshKey:              true,
	})
	require.True(t, created)
	require.Equal(t, "leader-b-1", fresh.Key)
	require.NotEqual(t, oldFollowerTag.Key, fresh.Key)

	follower := NewManager(Options{LocalNodeID: 2, TTL: time.Minute, Now: time.Now, NewTagKey: newSequenceKeyGen("unused").Next})
	require.True(t, mustStoreFollower(t, follower, oldFollowerTag))
	storedFresh, ok := follower.StoreFollowerPartition(fresh)
	require.True(t, ok)
	require.Equal(t, fresh.Key, storedFresh.Key)
	require.Equal(t, []string{"new"}, storedFresh.PartitionForNode(2).UIDs)

	staleOld := TagRef{ChannelKey: "group:zeta", TagKey: oldFollowerTag.Key, TagVersion: oldFollowerTag.TagVersion, SubscriberMutationVersion: oldFollowerTag.SubscriberMutationVersion, Topology: oldFollowerTag.Topology}
	_, hit, reason := follower.LookupLocalPartition(staleOld)
	require.False(t, hit)
	require.Equal(t, LookupTagKeyMismatch, reason)

	current, ok := follower.CurrentRef("group:zeta")
	require.True(t, ok)
	require.Equal(t, fresh.Key, current.TagKey)
}

func testTopology(tableVersion uint64, nodeIDs ...uint64) PartitionTopologyVersion {
	refs := make([]SlotAuthorityRef, 0, len(nodeIDs))
	for slotID, nodeID := range nodeIDs {
		refs = append(refs, SlotAuthorityRef{SlotID: uint32(slotID + 1), LeaderNodeID: nodeID, ConfigEpoch: tableVersion + uint64(slotID), BalanceVersion: uint64(slotID + 100)})
	}
	return PartitionTopologyVersion{HashSlotTableVersion: tableVersion, SlotAuthorityRefs: refs}
}

type sequenceKeyGen struct {
	prefix string
	n      int
}

func newSequenceKeyGen(prefix string) *sequenceKeyGen { return &sequenceKeyGen{prefix: prefix} }

func (g *sequenceKeyGen) Next() string {
	g.n++
	return fmt.Sprintf("%s-%d", g.prefix, g.n)
}

func mustStoreFollower(t *testing.T, manager *Manager, tag DeliveryTag) bool {
	t.Helper()
	_, ok := manager.StoreFollowerPartition(tag)
	return ok
}

func TestBuildEphemeralTagDoesNotReplaceCurrentRef(t *testing.T) {
	keys := newSequenceKeyGen("tag")
	manager := NewManager(Options{LocalNodeID: 1, TTL: time.Minute, Now: time.Now, NewTagKey: keys.Next})
	topology := testTopology(10, 1, 2)

	reusable, created := manager.BuildLeaderTag(BuildRequest{
		ChannelKey:                "group:ephemeral",
		SubscriberMutationVersion: 1,
		Topology:                  topology,
		Partitions:                []NodePartition{{NodeID: 1, UIDs: []string{"u1"}}},
	})
	require.True(t, created)

	ephemeral, created := manager.BuildEphemeralTag(BuildRequest{
		ChannelKey:                "group:ephemeral",
		SubscriberMutationVersion: 99,
		Topology:                  topology,
		Partitions:                []NodePartition{{NodeID: 1, UIDs: []string{"scoped"}}},
	})
	require.True(t, created)
	require.NotEqual(t, reusable.Key, ephemeral.Key)

	ref, ok := manager.CurrentRef("group:ephemeral")
	require.True(t, ok)
	require.Equal(t, reusable.Key, ref.TagKey)

	cached, ok := manager.LookupTag(ephemeral.Key)
	require.True(t, ok)
	require.Equal(t, []string{"scoped"}, cached.PartitionForNode(1).UIDs)
}
