package cluster

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestInspectLocalRestoreTargetChecksSemanticMetadataAndMessages(t *testing.T) {
	newNode := func(t *testing.T) *Node {
		t.Helper()
		base := t.TempDir()
		meta, err := metadb.Open(filepath.Join(base, "meta"))
		if err != nil {
			t.Fatalf("open meta DB: %v", err)
		}
		messages := channelstore.NewMessageDBFactory(filepath.Join(base, "messages"))
		node := &Node{
			cfg:               Config{NodeID: 1, RestoreMode: true, Slots: SlotConfig{HashSlotCount: 2}},
			defaultSlotMetaDB: meta, defaultChannelStore: messages,
		}
		t.Cleanup(func() {
			_ = messages.Close()
			_ = meta.Close()
		})
		return node
	}

	t.Run("empty target", func(t *testing.T) {
		node := newNode(t)
		state, err := node.InspectLocalRestoreTarget(context.Background())
		if err != nil || !state.Empty || !state.MetadataEmpty || !state.MessagesEmpty {
			t.Fatalf("InspectLocalRestoreTarget() = %#v, %v", state, err)
		}
	})

	t.Run("semantic metadata", func(t *testing.T) {
		node := newNode(t)
		if err := node.defaultSlotMetaDB.ForHashSlot(1).CreateUser(context.Background(), metadb.User{UID: "u1", Token: "token"}); err != nil {
			t.Fatalf("CreateUser(): %v", err)
		}
		state, err := node.InspectLocalRestoreTarget(context.Background())
		if err != nil || state.Empty || state.MetadataEmpty || !state.MessagesEmpty {
			t.Fatalf("InspectLocalRestoreTarget() = %#v, %v", state, err)
		}
	})

	t.Run("message catalog", func(t *testing.T) {
		node := newNode(t)
		id := channelruntime.ChannelID{ID: "room", Type: 2}
		store, err := node.defaultChannelStore.ChannelStore(channelruntime.ChannelKeyForID(id), id)
		if err != nil {
			t.Fatalf("ChannelStore(): %v", err)
		}
		if _, err := store.AppendLeader(context.Background(), channelstore.AppendLeaderRequest{
			Records: []channelruntime.Record{{ID: 1, Payload: []byte("message"), SizeBytes: len("message")}}, Sync: true,
		}); err != nil {
			t.Fatalf("AppendLeader(): %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
		state, err := node.InspectLocalRestoreTarget(context.Background())
		if err != nil || state.Empty || !state.MetadataEmpty || state.MessagesEmpty {
			t.Fatalf("InspectLocalRestoreTarget() = %#v, %v", state, err)
		}
	})
}

func TestRestoreHashSlotMetadataDigestFencesPostInstallVerification(t *testing.T) {
	base := t.TempDir()
	meta, err := metadb.Open(filepath.Join(base, "meta"))
	if err != nil {
		t.Fatalf("open meta DB: %v", err)
	}
	messages := channelstore.NewMessageDBFactory(filepath.Join(base, "messages"))
	node := &Node{
		cfg:               Config{NodeID: 1, RestoreMode: true, Slots: SlotConfig{HashSlotCount: 2}},
		defaultSlotMetaDB: meta, defaultChannelStore: messages,
	}
	t.Cleanup(func() {
		_ = messages.Close()
		_ = meta.Close()
	})
	if err := node.defaultSlotMetaDB.ForHashSlot(1).CreateUser(context.Background(), metadb.User{UID: "u1", Token: ""}); err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}
	digest, err := node.RestoreHashSlotMetadataDigest(context.Background(), 1)
	if err != nil {
		t.Fatalf("RestoreHashSlotMetadataDigest(): %v", err)
	}
	if len(digest) != 64 {
		t.Fatalf("digest length = %d", len(digest))
	}
	if err := node.VerifyLocalRestorePartition(context.Background(), 1, digest, nil); err != nil {
		t.Fatalf("VerifyLocalRestorePartition(valid digest): %v", err)
	}
	if err := node.VerifyLocalRestorePartition(context.Background(), 1, strings.Repeat("0", 64), nil); err == nil {
		t.Fatal("VerifyLocalRestorePartition(mismatched digest) error = nil")
	}
}

func TestInstallRestoreChannelRuntimeMetaBuildsTargetTopologyInBoundedBatch(t *testing.T) {
	base := t.TempDir()
	meta, err := metadb.Open(filepath.Join(base, "meta"))
	if err != nil {
		t.Fatalf("open meta DB: %v", err)
	}
	messages := channelstore.NewMessageDBFactory(filepath.Join(base, "messages"))
	router := routing.NewRouter()
	snapshot := control.Snapshot{
		Revision:     1,
		ControllerID: 1,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "127.0.0.1:1001", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 2, Addr: "127.0.0.1:1002", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 3, Addr: "127.0.0.1:1003", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots:     []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1, PreferredLeader: 2}},
		HashSlots: control.HashSlotTable{Revision: 1, Count: 2, Ranges: []control.HashSlotRange{{From: 0, To: 1, SlotID: 1}}},
	}
	if err := router.UpdateControlSnapshot(snapshot); err != nil {
		t.Fatalf("UpdateControlSnapshot(): %v", err)
	}
	router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 2, LeaderTerm: 1}})
	node := &Node{
		cfg: Config{
			NodeID: 1, RestoreMode: true,
			Slots:   SlotConfig{HashSlotCount: 2},
			Channel: ChannelConfig{ReplicaCount: 3},
		},
		router:              router,
		defaultSlotMetaDB:   meta,
		defaultChannelStore: messages,
	}
	node.channelDataNodes.Update([]uint64{1, 2, 3})
	t.Cleanup(func() {
		_ = messages.Close()
		_ = meta.Close()
	})

	hashSlot := routing.HashSlotForKey("room", 2)
	boundary := RestoreVerifyBoundary{ChannelID: "room", ChannelType: 2, Epoch: 7, LogStartOffset: 3, HW: 9}
	if err := node.VerifyLocalRestorePartition(context.Background(), hashSlot, "", []RestoreVerifyBoundary{boundary}); err == nil {
		t.Fatal("VerifyLocalRestorePartition(missing runtime metadata) error = nil")
	}
	if err := node.InstallRestoreChannelRuntimeMeta(context.Background(), hashSlot, []RestoreVerifyBoundary{boundary}); err != nil {
		t.Fatalf("InstallRestoreChannelRuntimeMeta() error = %v", err)
	}
	got, err := meta.ForHashSlot(hashSlot).GetChannelRuntimeMeta(context.Background(), "room", 2)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	if got.ChannelEpoch != 7 || got.LeaderEpoch != 1 || got.Leader != 2 || got.MinISR != 2 || got.RetentionThroughSeq != 3 || got.Status != uint8(channelruntime.StatusActive) {
		t.Fatalf("restored runtime meta = %#v", got)
	}
	if want := []uint64{1, 2, 3}; !equalUint64s(got.Replicas, want) || !equalUint64s(got.ISR, want) {
		t.Fatalf("restored runtime replicas=%v isr=%v, want %v", got.Replicas, got.ISR, want)
	}
	if err := node.InstallRestoreChannelRuntimeMeta(context.Background(), hashSlot^1, []RestoreVerifyBoundary{boundary}); err == nil {
		t.Fatal("InstallRestoreChannelRuntimeMeta(wrong hash slot) error = nil")
	}
}
