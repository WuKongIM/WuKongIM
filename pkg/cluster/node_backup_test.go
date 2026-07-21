package cluster

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
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
