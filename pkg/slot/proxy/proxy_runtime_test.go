package proxy

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestStoreUpsertAndGetChannelRuntimeMeta(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	bizDB := openTestDBAt(t, filepath.Join(root, "biz"))
	_, store := newSingleNodeProxyTestStore(t, bizDB, 1)
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    "store-meta",
		ChannelType:  9,
		ChannelEpoch: 11,
		LeaderEpoch:  6,
		Replicas:     []uint64{3, 1, 2},
		ISR:          []uint64{2, 1},
		Leader:       1,
		MinISR:       2,
		Status:       5,
		Features:     33,
		LeaseUntilMS: 1700000004321,
	}

	if err := store.UpsertChannelRuntimeMeta(ctx, meta); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta() error = %v", err)
	}

	got, err := store.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}

	want := metadb.NormalizeChannelRuntimeMeta(meta)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stored runtime meta = %#v, want %#v", got, want)
	}
}

func TestStoreAdvanceChannelRetentionThroughSeqPreservesTopology(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	bizDB := openTestDBAt(t, filepath.Join(root, "biz"))
	_, store := newSingleNodeProxyTestStore(t, bizDB, 1)
	base := metadb.ChannelRuntimeMeta{
		ChannelID:    "store-retention-advance",
		ChannelType:  9,
		ChannelEpoch: 11,
		LeaderEpoch:  6,
		Replicas:     []uint64{3, 1, 2},
		ISR:          []uint64{2, 1},
		Leader:       1,
		MinISR:       2,
		Status:       5,
		Features:     33,
		LeaseUntilMS: 1700000004321,
	}
	require.NoError(t, store.UpsertChannelRuntimeMeta(ctx, base))

	require.NoError(t, store.AdvanceChannelRetentionThroughSeq(ctx, metadb.ChannelRetentionAdvance{
		ChannelID:            base.ChannelID,
		ChannelType:          base.ChannelType,
		ExpectedChannelEpoch: base.ChannelEpoch,
		ExpectedLeaderEpoch:  base.LeaderEpoch,
		ExpectedLeader:       base.Leader,
		ExpectedLeaseUntilMS: base.LeaseUntilMS,
		RetentionThroughSeq:  42,
		RetentionUpdatedAtMS: 1700000005000,
	}))

	got, err := store.GetChannelRuntimeMeta(ctx, base.ChannelID, base.ChannelType)
	require.NoError(t, err)
	want := metadb.NormalizeChannelRuntimeMeta(base)
	want.RouteGeneration++
	want.RetentionThroughSeq = 42
	want.RetentionUpdatedAtMS = 1700000005000
	require.Equal(t, want, got)
}

func TestStoreStaleAdvanceChannelRetentionThroughSeqReturnsStaleMetaAndSlotStaysUsable(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	bizDB := openTestDBAt(t, filepath.Join(root, "biz"))
	_, store := newSingleNodeProxyTestStore(t, bizDB, 1)
	base := metadb.ChannelRuntimeMeta{
		ChannelID:    "store-stale-retention-advance",
		ChannelType:  9,
		ChannelEpoch: 11,
		LeaderEpoch:  6,
		Replicas:     []uint64{1, 2},
		ISR:          []uint64{1, 2},
		Leader:       1,
		MinISR:       1,
		Status:       2,
		LeaseUntilMS: 1700000004321,
	}
	require.NoError(t, store.UpsertChannelRuntimeMeta(ctx, base))

	current := base
	current.LeaderEpoch++
	current.LeaseUntilMS += 1000
	require.NoError(t, store.UpsertChannelRuntimeMeta(ctx, current))
	storedCurrent, err := store.GetChannelRuntimeMeta(ctx, current.ChannelID, current.ChannelType)
	require.NoError(t, err)

	err = store.AdvanceChannelRetentionThroughSeq(ctx, metadb.ChannelRetentionAdvance{
		ChannelID:            base.ChannelID,
		ChannelType:          base.ChannelType,
		ExpectedChannelEpoch: base.ChannelEpoch,
		ExpectedLeaderEpoch:  base.LeaderEpoch,
		ExpectedLeader:       base.Leader,
		ExpectedLeaseUntilMS: base.LeaseUntilMS,
		RetentionThroughSeq:  42,
		RetentionUpdatedAtMS: 1700000005000,
	})
	require.ErrorIs(t, err, metadb.ErrStaleMeta)

	require.NoError(t, store.AdvanceChannelRetentionThroughSeq(ctx, metadb.ChannelRetentionAdvance{
		ChannelID:            current.ChannelID,
		ChannelType:          current.ChannelType,
		ExpectedChannelEpoch: current.ChannelEpoch,
		ExpectedLeaderEpoch:  current.LeaderEpoch,
		ExpectedLeader:       current.Leader,
		ExpectedLeaseUntilMS: current.LeaseUntilMS,
		RetentionThroughSeq:  43,
		RetentionUpdatedAtMS: 1700000006000,
	}))

	got, err := store.GetChannelRuntimeMeta(ctx, current.ChannelID, current.ChannelType)
	require.NoError(t, err)
	want := storedCurrent
	want.RouteGeneration++
	want.RetentionThroughSeq = 43
	want.RetentionUpdatedAtMS = 1700000006000
	require.Equal(t, want, got)
}

func TestStoreCreateUserAndUpsertDevice(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	bizDB := openTestDBAt(t, filepath.Join(root, "biz"))
	_, store := newSingleNodeProxyTestStore(t, bizDB, 1)
	require.NoError(t, store.CreateUser(ctx, metadb.User{UID: "u1"}))

	gotUser, err := store.GetUser(ctx, "u1")
	require.NoError(t, err)
	require.Equal(t, "u1", gotUser.UID)

	err = store.CreateUser(ctx, metadb.User{UID: "u1", Token: "overwrite-attempt"})
	require.ErrorIs(t, err, metadb.ErrAlreadyExists)

	require.NoError(t, store.UpsertDevice(ctx, metadb.Device{
		UID:         "u1",
		DeviceFlag:  2,
		Token:       "web-token",
		DeviceLevel: 1,
	}))

	gotDevice, err := store.GetDevice(ctx, "u1", 2)
	require.NoError(t, err)
	require.Equal(t, metadb.Device{
		UID:         "u1",
		DeviceFlag:  2,
		Token:       "web-token",
		DeviceLevel: 1,
	}, gotDevice)
}

func TestStoreUpsertUserRoutesByHashSlotOnShardedCluster(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 8)

	uid := findUIDForSlotWithDifferentHashSlot(t, nodes[0].cluster, 2, 2, "hashslot-user")
	hashSlot := nodes[0].cluster.HashSlotForKey(uid)
	require.NotEqual(t, uint16(2), hashSlot)
	require.Equal(t, multiraft.SlotID(2), nodes[0].cluster.SlotForKey(uid))

	user := metadb.User{
		UID:   uid,
		Token: "hash-slot-token",
	}
	require.NoError(t, nodes[1].store.UpsertUser(ctx, user))

	got, err := nodes[1].db.ForHashSlot(hashSlot).GetUser(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, user, got)

	routed, err := nodes[0].store.GetUser(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, user, routed)
}

func TestStoreListChannelRuntimeMeta(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	bizDB := openTestDBAt(t, filepath.Join(root, "biz"))
	_, store := newSingleNodeProxyTestStore(t, bizDB, 2)
	first := metadb.ChannelRuntimeMeta{
		ChannelID:    "store-list-1",
		ChannelType:  1,
		ChannelEpoch: 11,
		LeaderEpoch:  6,
		Replicas:     []uint64{1, 2},
		ISR:          []uint64{1, 2},
		Leader:       1,
		MinISR:       1,
		Status:       2,
		Features:     1,
		LeaseUntilMS: 1700000000111,
	}
	second := metadb.ChannelRuntimeMeta{
		ChannelID:    "store-list-2",
		ChannelType:  1,
		ChannelEpoch: 12,
		LeaderEpoch:  7,
		Replicas:     []uint64{1, 3},
		ISR:          []uint64{1, 3},
		Leader:       1,
		MinISR:       1,
		Status:       2,
		Features:     2,
		LeaseUntilMS: 1700000000222,
	}

	require.NoError(t, store.UpsertChannelRuntimeMeta(ctx, first))
	require.NoError(t, store.UpsertChannelRuntimeMeta(ctx, second))

	got, err := store.ListChannelRuntimeMeta(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []metadb.ChannelRuntimeMeta{
		metadb.NormalizeChannelRuntimeMeta(first),
		metadb.NormalizeChannelRuntimeMeta(second),
	}, got)
}

func TestStoreGetChannelRuntimeMetaReadsAuthoritativeRemoteSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	channelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "remote-runtime")
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    channelID,
		ChannelType:  1,
		ChannelEpoch: 21,
		LeaderEpoch:  8,
		Replicas:     []uint64{1, 2},
		ISR:          []uint64{1, 2},
		Leader:       2,
		MinISR:       1,
		Status:       2,
		Features:     7,
		LeaseUntilMS: 1700000000999,
	}
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, channelID)).UpsertChannelRuntimeMeta(ctx, meta))

	got, err := nodes[0].store.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	require.NoError(t, err)
	require.Equal(t, metadb.NormalizeChannelRuntimeMeta(meta), got)
}

func TestStoreReadChannelRuntimeMetadataBatchPreservesSlotAndMissingAlignment(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)
	local := metadb.ChannelRuntimeMeta{
		ChannelID: findChannelIDForSlot(t, nodes[0].cluster, 1, "batch-local-runtime"), ChannelType: 2,
		ChannelEpoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1, Status: 2,
	}
	remote := metadb.ChannelRuntimeMeta{
		ChannelID: findChannelIDForSlot(t, nodes[0].cluster, 2, "batch-remote-runtime"), ChannelType: 2,
		ChannelEpoch: 2, LeaderEpoch: 2, Leader: 2, Replicas: []uint64{2}, ISR: []uint64{2}, MinISR: 1, Status: 2,
	}
	missing := metadb.ChannelKey{ChannelID: findChannelIDForSlot(t, nodes[0].cluster, 1, "batch-missing-runtime"), ChannelType: 2}
	require.NoError(t, nodes[0].db.ForHashSlot(mustHashSlotForKey(t, nodes[0].cluster, local.ChannelID)).UpsertChannelRuntimeMeta(ctx, local))
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, remote.ChannelID)).UpsertChannelRuntimeMeta(ctx, remote))

	results, err := nodes[0].store.ReadChannelRuntimeMetadataBatch(ctx, []metadb.ChannelKey{
		{ChannelID: local.ChannelID, ChannelType: local.ChannelType}, missing,
		{ChannelID: remote.ChannelID, ChannelType: remote.ChannelType},
	})

	require.NoError(t, err)
	require.Len(t, results, 3)
	require.NoError(t, results[0].Err)
	require.Equal(t, metadb.NormalizeChannelRuntimeMeta(local), results[0].Meta)
	require.ErrorIs(t, results[1].Err, metadb.ErrNotFound)
	require.NoError(t, results[2].Err)
	require.Equal(t, metadb.NormalizeChannelRuntimeMeta(remote), results[2].Meta)
}

func TestStoreGetChannelForPermissionReadsAuthoritativeSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	channelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "remote-channel-permission")
	ch := metadb.Channel{
		ChannelID: channelID, ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1,
		AllowStranger: 1,
	}
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, channelID)).UpsertChannel(ctx, ch))

	got, err := nodes[0].store.GetChannelForPermission(ctx, channelID, 2)
	require.NoError(t, err)
	require.Equal(t, ch, got)
}

func TestStoreReadPermissionMetadataBatchRoutesBySlotAndPreservesAlignment(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	localChannelID := findChannelIDForSlot(t, nodes[0].cluster, 1, "local-permission-batch")
	remoteChannelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "remote-permission-batch")
	localChannel := metadb.Channel{ChannelID: localChannelID, ChannelType: 2, Ban: 1}
	remoteChannel := metadb.Channel{ChannelID: remoteChannelID, ChannelType: 2, Disband: 1}
	require.NoError(t, nodes[0].db.ForHashSlot(mustHashSlotForKey(t, nodes[0].cluster, localChannelID)).UpsertChannel(ctx, localChannel))
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, remoteChannelID)).UpsertChannel(ctx, remoteChannel))
	for nodeIndex, channelID := range []string{localChannelID, remoteChannelID} {
		shard, ok := any(nodes[nodeIndex].db.ForHashSlot(mustHashSlotForKey(t, nodes[nodeIndex].cluster, channelID))).(interface {
			AddSubscribers(context.Context, string, int64, []string, ...uint64) error
		})
		require.True(t, ok)
		require.NoError(t, shard.AddSubscribers(ctx, channelID, 2, []string{"u1"}))
	}

	results := nodes[0].store.ReadPermissionMetadataBatch(ctx, []PermissionMetadataRead{
		{Kind: PermissionMetadataReadChannel, ChannelID: remoteChannelID, ChannelType: 2},
		{Kind: PermissionMetadataReadSubscriberContains, ChannelID: localChannelID, ChannelType: 2, UID: "u1"},
		{Kind: PermissionMetadataReadSubscriberContains, ChannelID: remoteChannelID, ChannelType: 2, UID: "missing"},
		{Kind: PermissionMetadataReadChannel, ChannelID: localChannelID + "-missing", ChannelType: 2},
		{Kind: PermissionMetadataReadSubscriberHasAny, ChannelID: remoteChannelID, ChannelType: 2},
	})

	require.Len(t, results, 5)
	for _, result := range results {
		require.NoError(t, result.Err)
	}
	require.True(t, results[0].Found)
	expectedRemoteChannel := remoteChannel
	expectedRemoteChannel.SubscriberCount = 1
	require.Equal(t, expectedRemoteChannel, results[0].Channel)
	require.True(t, results[1].Value)
	require.False(t, results[2].Value)
	require.False(t, results[3].Found)
	require.True(t, results[4].Value)
}

func TestStoreListChannelSubscribersReadsAuthoritativeSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	channelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "remote-subscribers")

	remoteShard, ok := any(nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, channelID))).(interface {
		AddSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error
	})
	require.True(t, ok, "subscriber shard store methods missing")
	require.NoError(t, remoteShard.AddSubscribers(ctx, channelID, 2, []string{"u3", "u1", "u2"}))

	store, ok := any(nodes[0].store).(interface {
		ListChannelSubscribers(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error)
	})
	require.True(t, ok, "subscriber store methods missing")

	page1, cursor, done, err := store.ListChannelSubscribers(ctx, channelID, 2, "", 2)
	require.NoError(t, err)
	require.Equal(t, []string{"u1", "u2"}, page1)
	require.Equal(t, "u2", cursor)
	require.False(t, done)

	page2, cursor, done, err := store.ListChannelSubscribers(ctx, channelID, 2, cursor, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"u3"}, page2)
	require.Empty(t, cursor)
	require.True(t, done)
}

func TestStoreCountedSubscriberMutationsReturnDurableSetChanges(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	channelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "remote-counted-subscribers")
	require.NoError(t, nodes[0].store.UpsertChannel(ctx, metadb.Channel{ChannelID: channelID, ChannelType: 2}))

	added, err := nodes[0].store.AddChannelSubscribersCounted(ctx, channelID, 2, []string{"u1", "u1", "u2"}, 1)
	require.NoError(t, err)
	require.Equal(t, metadb.SubscriberMutationResult{RequestedCount: 2, ChangedCount: 2}, added)

	added, err = nodes[0].store.AddChannelSubscribersCounted(ctx, channelID, 2, []string{"u1", "u3"}, 2)
	require.NoError(t, err)
	require.Equal(t, metadb.SubscriberMutationResult{RequestedCount: 2, ChangedCount: 1}, added)

	removed, err := nodes[0].store.RemoveChannelSubscribersCounted(ctx, channelID, 2, []string{"missing", "u2"}, 3)
	require.NoError(t, err)
	require.Equal(t, metadb.SubscriberMutationResult{RequestedCount: 2, ChangedCount: 1}, removed)
}

func TestStoreConditionalChannelMutationsAreAuthoritativeAndPreserveSubscriberMetadata(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	channelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "remote-conditional-channel")
	channel := metadb.Channel{
		ChannelID: channelID, ChannelType: 2, Ban: 1, AllowStranger: 1,
	}
	require.NoError(t, nodes[0].store.CreateChannelMetadata(ctx, channel))
	require.ErrorIs(t, nodes[0].store.CreateChannelMetadata(ctx, channel), metadb.ErrAlreadyExists)
	_, err := nodes[0].store.AddChannelSubscribersCounted(ctx, channelID, 2, []string{"u1"}, 7)
	require.NoError(t, err)

	require.NoError(t, nodes[0].store.PatchChannelBusinessFlags(ctx, channelID, 2, metadb.ChannelBusinessFlags{
		Disband: 1, SendBan: 1,
	}))
	got, err := nodes[0].store.GetChannelForPermission(ctx, channelID, 2)
	require.NoError(t, err)
	require.Equal(t, int64(0), got.Ban)
	require.Equal(t, int64(1), got.Disband)
	require.Equal(t, int64(1), got.SendBan)
	require.Equal(t, int64(1), got.AllowStranger)
	require.Equal(t, uint64(7), got.SubscriberMutationVersion)
	require.Equal(t, uint64(1), got.SubscriberCount)
	require.ErrorIs(
		t,
		nodes[0].store.PatchChannelBusinessFlags(ctx, "missing-channel", 2, metadb.ChannelBusinessFlags{Ban: 1}),
		metadb.ErrNotFound,
	)
}

func TestStoreSnapshotChannelSubscribersReadsAuthoritativeSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	channelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "remote-subscriber-snapshot")

	remoteShard, ok := any(nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, channelID))).(interface {
		AddSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error
	})
	require.True(t, ok, "subscriber shard store methods missing")
	require.NoError(t, remoteShard.AddSubscribers(ctx, channelID, 2, []string{"u3", "u1", "u2"}))

	store, ok := any(nodes[0].store).(interface {
		SnapshotChannelSubscribers(ctx context.Context, channelID string, channelType int64) ([]string, error)
	})
	require.True(t, ok, "subscriber snapshot store methods missing")

	snapshot, err := store.SnapshotChannelSubscribers(ctx, channelID, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"u1", "u2", "u3"}, snapshot)
}

func TestStoreContainsChannelSubscriberReadsAuthoritativeSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	channelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "remote-subscriber-contains")

	remoteShard, ok := any(nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, channelID))).(interface {
		AddSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error
	})
	require.True(t, ok, "subscriber shard store methods missing")
	require.NoError(t, remoteShard.AddSubscribers(ctx, channelID, 2, []string{"u1"}))

	ok, err := nodes[0].store.ContainsChannelSubscriber(ctx, channelID, 2, "u1")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = nodes[0].store.ContainsChannelSubscriber(ctx, channelID, 2, "missing")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestStoreHasChannelSubscribersReadsAuthoritativeSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	channelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "remote-subscriber-has-any")

	ok, err := nodes[0].store.HasChannelSubscribers(ctx, channelID, 2)
	require.NoError(t, err)
	require.False(t, ok)

	remoteShard, ok := any(nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, channelID))).(interface {
		AddSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error
	})
	require.True(t, ok, "subscriber shard store methods missing")
	require.NoError(t, remoteShard.AddSubscribers(ctx, channelID, 2, []string{"u1"}))

	ok, err = nodes[0].store.HasChannelSubscribers(ctx, channelID, 2)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestStoreGetUserReadsAuthoritativeRemoteSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	uid := findUIDForSlot(t, nodes[0].cluster, 2, "remote-user")
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, uid)).CreateUser(ctx, metadb.User{
		UID:         uid,
		Token:       "remote-token",
		DeviceFlag:  3,
		DeviceLevel: 7,
	}))

	got, err := nodes[0].store.GetUser(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, metadb.User{
		UID:         uid,
		Token:       "remote-token",
		DeviceFlag:  3,
		DeviceLevel: 7,
	}, got)
}

func TestStoreGetDeviceReadsAuthoritativeRemoteSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	uid := findUIDForSlot(t, nodes[0].cluster, 2, "remote-device")
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, uid)).UpsertDevice(ctx, metadb.Device{
		UID:         uid,
		DeviceFlag:  5,
		Token:       "device-token",
		DeviceLevel: 1,
	}))

	got, err := nodes[0].store.GetDevice(ctx, uid, 5)
	require.NoError(t, err)
	require.Equal(t, metadb.Device{
		UID:         uid,
		DeviceFlag:  5,
		Token:       "device-token",
		DeviceLevel: 1,
	}, got)
}

func TestStoreCreateUserReturnsAlreadyExistsForAuthoritativeRemoteSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	uid := findUIDForSlot(t, nodes[0].cluster, 2, "remote-create")
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, uid)).CreateUser(ctx, metadb.User{UID: uid}))

	err := nodes[0].store.CreateUser(ctx, metadb.User{UID: uid})
	require.ErrorIs(t, err, metadb.ErrAlreadyExists)
}

func TestStoreListChannelRuntimeMetaReadsAuthoritativeAllSlots(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	first := metadb.ChannelRuntimeMeta{
		ChannelID:    findChannelIDForSlot(t, nodes[0].cluster, 1, "slot-one"),
		ChannelType:  1,
		ChannelEpoch: 31,
		LeaderEpoch:  11,
		Replicas:     []uint64{1},
		ISR:          []uint64{1},
		Leader:       1,
		MinISR:       1,
		Status:       2,
		Features:     1,
		LeaseUntilMS: 1700000001111,
	}
	second := metadb.ChannelRuntimeMeta{
		ChannelID:    findChannelIDForSlot(t, nodes[0].cluster, 2, "slot-two"),
		ChannelType:  1,
		ChannelEpoch: 32,
		LeaderEpoch:  12,
		Replicas:     []uint64{2},
		ISR:          []uint64{2},
		Leader:       2,
		MinISR:       1,
		Status:       2,
		Features:     2,
		LeaseUntilMS: 1700000002222,
	}
	require.NoError(t, nodes[0].db.ForHashSlot(mustHashSlotForKey(t, nodes[0].cluster, first.ChannelID)).UpsertChannelRuntimeMeta(ctx, first))
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, second.ChannelID)).UpsertChannelRuntimeMeta(ctx, second))

	got, err := nodes[0].store.ListChannelRuntimeMeta(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []metadb.ChannelRuntimeMeta{
		metadb.NormalizeChannelRuntimeMeta(first),
		metadb.NormalizeChannelRuntimeMeta(second),
	}, got)
}
