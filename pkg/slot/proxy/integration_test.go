//go:build integration
// +build integration

package proxy

import (
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func proposalPayload(data []byte) []byte {
	payload := make([]byte, 2+len(data))
	binary.BigEndian.PutUint16(payload[:2], 0)
	copy(payload[2:], data)
	return payload
}

func TestMemoryBackedGroupAppliesProposalToWKDB(t *testing.T) {
	ctx := context.Background()
	slotID := multiraft.SlotID(51)
	db := openTestDB(t)
	rt := newStartedRuntime(t)

	if err := rt.BootstrapSlot(ctx, multiraft.BootstrapSlotRequest{
		Slot: multiraft.SlotOptions{
			ID:           slotID,
			Storage:      raftstorage.NewMemory(),
			StateMachine: mustNewStateMachine(t, db, uint64(slotID)),
		},
		Voters: []multiraft.NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == multiraft.RoleLeader
	}, "slot become leader")

	fut, err := rt.Propose(ctx, slotID, proposalPayload(metafsm.EncodeUpsertUserCommand(metadb.User{
		UID:         "u1",
		Token:       "t1",
		DeviceFlag:  1,
		DeviceLevel: 2,
	})))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	got, err := db.ForSlot(uint64(slotID)).GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got.Token != "t1" {
		t.Fatalf("stored user = %#v", got)
	}
}

func TestMemoryBackedGroupDoesNotRecoverDeletedSlotDataAfterOpenGroup(t *testing.T) {
	ctx := context.Background()
	slotID := multiraft.SlotID(51)
	db := openTestDB(t)

	rt := newStartedRuntime(t)
	if err := rt.BootstrapSlot(ctx, multiraft.BootstrapSlotRequest{
		Slot: multiraft.SlotOptions{
			ID:           slotID,
			Storage:      raftstorage.NewMemory(),
			StateMachine: mustNewStateMachine(t, db, uint64(slotID)),
		},
		Voters: []multiraft.NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == multiraft.RoleLeader
	}, "slot become leader")

	fut, err := rt.Propose(ctx, slotID, proposalPayload(metafsm.EncodeUpsertUserCommand(metadb.User{
		UID:   "u1",
		Token: "t1",
	})))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := db.DeleteSlotData(ctx, uint64(slotID)); err != nil {
		t.Fatalf("DeleteSlotData() error = %v", err)
	}
	if _, err := db.ForSlot(uint64(slotID)).GetUser(ctx, "u1"); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetUser() after delete err = %v, want ErrNotFound", err)
	}

	reopenRT := newStartedRuntime(t)
	if err := reopenRT.OpenSlot(ctx, multiraft.SlotOptions{
		ID:           slotID,
		Storage:      raftstorage.NewMemory(),
		StateMachine: mustNewStateMachine(t, db, uint64(slotID)),
	}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	_, err = db.ForSlot(uint64(slotID)).GetUser(ctx, "u1")
	if !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetUser() after reopen err = %v, want ErrNotFound", err)
	}
}

func TestMemoryBackedGroupReopensWithRecoveredMembership(t *testing.T) {
	ctx := context.Background()
	slotID := multiraft.SlotID(52)
	db := openTestDB(t)
	store := raftstorage.NewMemory()

	rt := newStartedRuntime(t)
	if err := rt.BootstrapSlot(ctx, multiraft.BootstrapSlotRequest{
		Slot: multiraft.SlotOptions{
			ID:           slotID,
			Storage:      store,
			StateMachine: mustNewStateMachine(t, db, uint64(slotID)),
		},
		Voters: []multiraft.NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == multiraft.RoleLeader
	}, "slot become leader")

	fut, err := rt.Propose(ctx, slotID, proposalPayload(metafsm.EncodeUpsertUserCommand(metadb.User{
		UID:   "u1",
		Token: "before-reopen",
	})))
	if err != nil {
		t.Fatalf("Propose(before reopen) error = %v", err)
	}
	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait(before reopen) error = %v", err)
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopenRT := newStartedRuntime(t)
	if err := reopenRT.OpenSlot(ctx, multiraft.SlotOptions{
		ID:           slotID,
		Storage:      store,
		StateMachine: mustNewStateMachine(t, db, uint64(slotID)),
	}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := reopenRT.Status(slotID)
		return err == nil && st.Role == multiraft.RoleLeader
	}, "reopened slot become leader")

	fut, err = reopenRT.Propose(ctx, slotID, proposalPayload(metafsm.EncodeUpsertUserCommand(metadb.User{
		UID:   "u1",
		Token: "after-reopen",
	})))
	if err != nil {
		t.Fatalf("Propose(after reopen) error = %v", err)
	}
	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait(after reopen) error = %v", err)
	}

	got, err := db.ForSlot(uint64(slotID)).GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got.Token != "after-reopen" {
		t.Fatalf("stored user = %#v", got)
	}
}

func TestPebbleBackedGroupReopensAndAcceptsNewProposal(t *testing.T) {
	ctx := context.Background()
	slotID := multiraft.SlotID(61)
	root := t.TempDir()
	bizPath := filepath.Join(root, "biz")
	raftPath := filepath.Join(root, "raft")

	bizDB := openTestDBAt(t, bizPath)
	raftDB := openTestRaftDBAt(t, raftPath)

	rt := newStartedRuntime(t)
	if err := rt.BootstrapSlot(ctx, multiraft.BootstrapSlotRequest{
		Slot: multiraft.SlotOptions{
			ID:           slotID,
			Storage:      raftDB.ForSlot(uint64(slotID)),
			StateMachine: mustNewStateMachine(t, bizDB, uint64(slotID)),
		},
		Voters: []multiraft.NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == multiraft.RoleLeader
	}, "slot become leader")

	fut, err := rt.Propose(ctx, slotID, proposalPayload(metafsm.EncodeUpsertUserCommand(metadb.User{
		UID:   "u1",
		Token: "before-reopen",
	})))
	if err != nil {
		t.Fatalf("Propose(before reopen) error = %v", err)
	}
	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait(before reopen) error = %v", err)
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := bizDB.Close(); err != nil {
		t.Fatalf("bizDB.Close() error = %v", err)
	}
	if err := raftDB.Close(); err != nil {
		t.Fatalf("raftDB.Close() error = %v", err)
	}

	reopenedBizDB := openTestDBAt(t, bizPath)
	reopenedRaftDB := openTestRaftDBAt(t, raftPath)
	reopenRT := newStartedRuntime(t)
	if err := reopenRT.OpenSlot(ctx, multiraft.SlotOptions{
		ID:           slotID,
		Storage:      reopenedRaftDB.ForSlot(uint64(slotID)),
		StateMachine: mustNewStateMachine(t, reopenedBizDB, uint64(slotID)),
	}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := reopenRT.Status(slotID)
		return err == nil && st.Role == multiraft.RoleLeader
	}, "reopened slot become leader")

	fut, err = reopenRT.Propose(ctx, slotID, proposalPayload(metafsm.EncodeUpsertUserCommand(metadb.User{
		UID:   "u1",
		Token: "after-reopen",
	})))
	if err != nil {
		t.Fatalf("Propose(after reopen) error = %v", err)
	}
	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait(after reopen) error = %v", err)
	}

	got, err := reopenedBizDB.ForSlot(uint64(slotID)).GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got.Token != "after-reopen" {
		t.Fatalf("stored user = %#v", got)
	}
}

func TestStoreUpsertAndGetChannelRuntimeMeta(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	bizDB := openTestDBAt(t, filepath.Join(root, "biz"))
	raftDB := openTestRaftDBAt(t, filepath.Join(root, "raft"))

	cluster, err := raftcluster.NewCluster(raftcluster.Config{
		NodeID:             1,
		ListenAddr:         "127.0.0.1:0",
		SlotCount:          1,
		ControllerReplicaN: 1,
		SlotReplicaN:       1,
		NewStorage: func(slotID multiraft.SlotID) (multiraft.Storage, error) {
			return raftDB.ForSlot(uint64(slotID)), nil
		},
		NewStateMachine:              metafsm.NewStateMachineFactory(bizDB),
		NewStateMachineWithHashSlots: metafsm.NewHashSlotStateMachineFactory(bizDB),
		Nodes:                        []raftcluster.NodeConfig{{NodeID: 1, Addr: "127.0.0.1:0"}},
		Slots: []raftcluster.SlotConfig{{
			SlotID: 1,
			Peers:  []multiraft.NodeID{1},
		}},
	})
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}
	t.Cleanup(cluster.Stop)
	if err := cluster.Start(); err != nil {
		t.Fatalf("cluster.Start() error = %v", err)
	}

	waitForCondition(t, func() bool {
		_, err := cluster.LeaderOf(1)
		return err == nil
	}, "cluster leader elected")

	store := New(cluster, bizDB)
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

	want := meta
	want.Replicas = []uint64{1, 2, 3}
	want.ISR = []uint64{1, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stored runtime meta = %#v, want %#v", got, want)
	}
}

func TestStoreCreateUserAndUpsertDevice(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	bizDB := openTestDBAt(t, filepath.Join(root, "biz"))
	raftDB := openTestRaftDBAt(t, filepath.Join(root, "raft"))

	cluster, err := raftcluster.NewCluster(raftcluster.Config{
		NodeID:             1,
		ListenAddr:         "127.0.0.1:0",
		SlotCount:          1,
		ControllerReplicaN: 1,
		SlotReplicaN:       1,
		NewStorage: func(slotID multiraft.SlotID) (multiraft.Storage, error) {
			return raftDB.ForSlot(uint64(slotID)), nil
		},
		NewStateMachine:              metafsm.NewStateMachineFactory(bizDB),
		NewStateMachineWithHashSlots: metafsm.NewHashSlotStateMachineFactory(bizDB),
		Nodes:                        []raftcluster.NodeConfig{{NodeID: 1, Addr: "127.0.0.1:0"}},
		Slots: []raftcluster.SlotConfig{{
			SlotID: 1,
			Peers:  []multiraft.NodeID{1},
		}},
	})
	require.NoError(t, err)
	t.Cleanup(cluster.Stop)
	require.NoError(t, cluster.Start())

	waitForCondition(t, func() bool {
		_, err := cluster.LeaderOf(1)
		return err == nil
	}, "cluster leader elected")

	store := New(cluster, bizDB)
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
	raftDB := openTestRaftDBAt(t, filepath.Join(root, "raft"))

	cluster, err := raftcluster.NewCluster(raftcluster.Config{
		NodeID:             1,
		ListenAddr:         "127.0.0.1:0",
		SlotCount:          2,
		ControllerReplicaN: 1,
		SlotReplicaN:       1,
		NewStorage: func(slotID multiraft.SlotID) (multiraft.Storage, error) {
			return raftDB.ForSlot(uint64(slotID)), nil
		},
		NewStateMachine:              metafsm.NewStateMachineFactory(bizDB),
		NewStateMachineWithHashSlots: metafsm.NewHashSlotStateMachineFactory(bizDB),
		Nodes:                        []raftcluster.NodeConfig{{NodeID: 1, Addr: "127.0.0.1:0"}},
		Slots: []raftcluster.SlotConfig{
			{SlotID: 1, Peers: []multiraft.NodeID{1}},
			{SlotID: 2, Peers: []multiraft.NodeID{1}},
		},
	})
	require.NoError(t, err)
	t.Cleanup(cluster.Stop)
	require.NoError(t, cluster.Start())

	waitForCondition(t, func() bool {
		_, err1 := cluster.LeaderOf(1)
		_, err2 := cluster.LeaderOf(2)
		return err1 == nil && err2 == nil
	}, "cluster leader elected")

	store := New(cluster, bizDB)
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
	require.ElementsMatch(t, []metadb.ChannelRuntimeMeta{first, second}, got)
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
	require.Equal(t, meta, got)
}

func TestStoreListChannelSubscribersReadsAuthoritativeSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	channelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "remote-subscribers")

	remoteShard, ok := any(nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, channelID))).(interface {
		AddSubscribers(ctx context.Context, channelID string, channelType int64, uids []string) error
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
	require.Equal(t, "u3", cursor)
	require.True(t, done)
}

func TestStoreSnapshotChannelSubscribersReadsAuthoritativeSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	channelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "remote-subscriber-snapshot")

	remoteShard, ok := any(nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, channelID))).(interface {
		AddSubscribers(ctx context.Context, channelID string, channelType int64, uids []string) error
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
	require.ElementsMatch(t, []metadb.ChannelRuntimeMeta{first, second}, got)
}

func TestStoreListUserConversationActiveReadsAuthoritativeSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	uid := findUIDForSlot(t, nodes[0].cluster, 2, "remote-active")
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, uid)).UpsertUserConversationState(ctx, metadb.UserConversationState{
		UID:         uid,
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    100,
		UpdatedAt:   10,
	}))
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, uid)).UpsertUserConversationState(ctx, metadb.UserConversationState{
		UID:         uid,
		ChannelID:   "g2",
		ChannelType: 2,
		ActiveAt:    300,
		UpdatedAt:   30,
	}))
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, uid)).UpsertUserConversationState(ctx, metadb.UserConversationState{
		UID:         uid,
		ChannelID:   "g3",
		ChannelType: 2,
		ActiveAt:    200,
		UpdatedAt:   20,
	}))

	store, ok := any(nodes[0].store).(interface {
		ListUserConversationActive(ctx context.Context, uid string, limit int) ([]metadb.UserConversationState, error)
	})
	require.True(t, ok, "conversation store methods missing")

	got, err := store.ListUserConversationActive(ctx, uid, 10)
	require.NoError(t, err)
	require.Equal(t, []metadb.UserConversationState{
		{UID: uid, ChannelID: "g2", ChannelType: 2, ActiveAt: 300, UpdatedAt: 30},
		{UID: uid, ChannelID: "g3", ChannelType: 2, ActiveAt: 200, UpdatedAt: 20},
		{UID: uid, ChannelID: "g1", ChannelType: 2, ActiveAt: 100, UpdatedAt: 10},
	}, got)
}

func TestStoreScanUserConversationStatePageReadsAuthoritativeSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	uid := findUIDForSlot(t, nodes[0].cluster, 2, "remote-scan")
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, uid)).UpsertUserConversationState(ctx, metadb.UserConversationState{
		UID:         uid,
		ChannelID:   "g1",
		ChannelType: 2,
		UpdatedAt:   10,
	}))
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, uid)).UpsertUserConversationState(ctx, metadb.UserConversationState{
		UID:         uid,
		ChannelID:   "g2",
		ChannelType: 2,
		UpdatedAt:   20,
	}))
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, uid)).UpsertUserConversationState(ctx, metadb.UserConversationState{
		UID:         uid,
		ChannelID:   "g3",
		ChannelType: 2,
		UpdatedAt:   30,
	}))

	store, ok := any(nodes[0].store).(interface {
		ScanUserConversationStatePage(ctx context.Context, uid string, after metadb.ConversationCursor, limit int) ([]metadb.UserConversationState, metadb.ConversationCursor, bool, error)
	})
	require.True(t, ok, "conversation store methods missing")

	page1, cursor, done, err := store.ScanUserConversationStatePage(ctx, uid, metadb.ConversationCursor{}, 2)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, []metadb.UserConversationState{
		{UID: uid, ChannelID: "g1", ChannelType: 2, UpdatedAt: 10},
		{UID: uid, ChannelID: "g2", ChannelType: 2, UpdatedAt: 20},
	}, page1)
	require.Equal(t, metadb.ConversationCursor{ChannelID: "g2", ChannelType: 2}, cursor)

	page2, cursor, done, err := store.ScanUserConversationStatePage(ctx, uid, cursor, 2)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, []metadb.UserConversationState{
		{UID: uid, ChannelID: "g3", ChannelType: 2, UpdatedAt: 30},
	}, page2)
	require.Equal(t, metadb.ConversationCursor{ChannelID: "g3", ChannelType: 2}, cursor)
}

func TestStoreBatchGetChannelUpdateLogsGroupsByChannelSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	slot1ChannelID := findChannelIDForSlot(t, nodes[0].cluster, 1, "slot1-update")
	slot2ChannelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "slot2-update")

	require.NoError(t, nodes[0].db.ForHashSlot(mustHashSlotForKey(t, nodes[0].cluster, slot1ChannelID)).UpsertChannelUpdateLog(ctx, metadb.ChannelUpdateLog{
		ChannelID:       slot1ChannelID,
		ChannelType:     1,
		UpdatedAt:       101,
		LastMsgSeq:      11,
		LastClientMsgNo: "c1",
		LastMsgAt:       201,
	}))
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, slot2ChannelID)).UpsertChannelUpdateLog(ctx, metadb.ChannelUpdateLog{
		ChannelID:       slot2ChannelID,
		ChannelType:     1,
		UpdatedAt:       102,
		LastMsgSeq:      12,
		LastClientMsgNo: "c2",
		LastMsgAt:       202,
	}))

	store, ok := any(nodes[0].store).(interface {
		BatchGetChannelUpdateLogs(ctx context.Context, keys []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelUpdateLog, error)
	})
	require.True(t, ok, "channel update log store methods missing")

	got, err := store.BatchGetChannelUpdateLogs(ctx, []metadb.ConversationKey{
		{ChannelID: slot1ChannelID, ChannelType: 1},
		{ChannelID: slot2ChannelID, ChannelType: 1},
	})
	require.NoError(t, err)
	require.Equal(t, map[metadb.ConversationKey]metadb.ChannelUpdateLog{
		{ChannelID: slot1ChannelID, ChannelType: 1}: {
			ChannelID:       slot1ChannelID,
			ChannelType:     1,
			UpdatedAt:       101,
			LastMsgSeq:      11,
			LastClientMsgNo: "c1",
			LastMsgAt:       201,
		},
		{ChannelID: slot2ChannelID, ChannelType: 1}: {
			ChannelID:       slot2ChannelID,
			ChannelType:     1,
			UpdatedAt:       102,
			LastMsgSeq:      12,
			LastClientMsgNo: "c2",
			LastMsgAt:       202,
		},
	}, got)
}

func TestStoreTouchUserConversationActiveAtGroupsByUIDSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	localUID := findUIDForSlot(t, nodes[0].cluster, 1, "local-touch")
	remoteUID := findUIDForSlot(t, nodes[0].cluster, 2, "remote-touch")
	localHashSlot := nodes[0].cluster.HashSlotForKey(localUID)
	remoteHashSlot := nodes[0].cluster.HashSlotForKey(remoteUID)
	require.NoError(t, nodes[0].db.ForHashSlot(localHashSlot).UpsertUserConversationState(ctx, metadb.UserConversationState{
		UID:         localUID,
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    100,
		UpdatedAt:   10,
	}))
	require.NoError(t, nodes[1].db.ForHashSlot(remoteHashSlot).UpsertUserConversationState(ctx, metadb.UserConversationState{
		UID:         remoteUID,
		ChannelID:   "g2",
		ChannelType: 2,
		ActiveAt:    200,
		UpdatedAt:   20,
	}))

	store, ok := any(nodes[0].store).(interface {
		TouchUserConversationActiveAt(ctx context.Context, patches []metadb.UserConversationActivePatch) error
	})
	require.True(t, ok, "conversation store methods missing")

	require.NoError(t, store.TouchUserConversationActiveAt(ctx, []metadb.UserConversationActivePatch{
		{UID: localUID, ChannelID: "g1", ChannelType: 2, ActiveAt: 300},
		{UID: remoteUID, ChannelID: "g2", ChannelType: 2, ActiveAt: 250},
	}))

	got, err := nodes[0].db.ForHashSlot(localHashSlot).GetUserConversationState(ctx, localUID, "g1", 2)
	require.NoError(t, err)
	require.Equal(t, int64(300), got.ActiveAt)
	require.Equal(t, int64(10), got.UpdatedAt)

	got, err = nodes[1].db.ForHashSlot(remoteHashSlot).GetUserConversationState(ctx, remoteUID, "g2", 2)
	require.NoError(t, err)
	require.Equal(t, int64(250), got.ActiveAt)
	require.Equal(t, int64(20), got.UpdatedAt)
}

func TestPebbleBackedGroupDoesNotRecoverDeletedBusinessStateWithoutSnapshot(t *testing.T) {
	ctx := context.Background()
	slotID := multiraft.SlotID(62)
	root := t.TempDir()
	bizPath := filepath.Join(root, "biz")
	raftPath := filepath.Join(root, "raft")

	bizDB := openTestDBAt(t, bizPath)
	raftDB := openTestRaftDBAt(t, raftPath)

	rt := newStartedRuntime(t)
	if err := rt.BootstrapSlot(ctx, multiraft.BootstrapSlotRequest{
		Slot: multiraft.SlotOptions{
			ID:           slotID,
			Storage:      raftDB.ForSlot(uint64(slotID)),
			StateMachine: mustNewStateMachine(t, bizDB, uint64(slotID)),
		},
		Voters: []multiraft.NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == multiraft.RoleLeader
	}, "slot become leader")

	fut, err := rt.Propose(ctx, slotID, proposalPayload(metafsm.EncodeUpsertUserCommand(metadb.User{
		UID:   "u1",
		Token: "t1",
	})))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := bizDB.Close(); err != nil {
		t.Fatalf("bizDB.Close() error = %v", err)
	}
	if err := raftDB.Close(); err != nil {
		t.Fatalf("raftDB.Close() error = %v", err)
	}

	reopenedBizDB := openTestDBAt(t, bizPath)
	reopenedRaftDB := openTestRaftDBAt(t, raftPath)

	if err := reopenedBizDB.DeleteSlotData(ctx, uint64(slotID)); err != nil {
		t.Fatalf("DeleteSlotData() error = %v", err)
	}
	if _, err := reopenedBizDB.ForSlot(uint64(slotID)).GetUser(ctx, "u1"); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetUser() after delete err = %v, want ErrNotFound", err)
	}

	reopenRT := newStartedRuntime(t)
	if err := reopenRT.OpenSlot(ctx, multiraft.SlotOptions{
		ID:           slotID,
		Storage:      reopenedRaftDB.ForSlot(uint64(slotID)),
		StateMachine: mustNewStateMachine(t, reopenedBizDB, uint64(slotID)),
	}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	if _, err := reopenedBizDB.ForSlot(uint64(slotID)).GetUser(ctx, "u1"); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetUser() after reopen err = %v, want ErrNotFound", err)
	}
}
