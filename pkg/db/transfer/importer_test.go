package transfer

import (
	"context"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db"
	msgdb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestNormalizeImportOptions(t *testing.T) {
	defaulted := normalizeImportOptions(ImportOptions{})
	if defaulted.SubscriberBatchSize != defaultSubscriberBatchSize {
		t.Fatalf("SubscriberBatchSize = %d, want %d", defaulted.SubscriberBatchSize, defaultSubscriberBatchSize)
	}
	if defaulted.MessageBatchSize != defaultMessageBatchSize {
		t.Fatalf("MessageBatchSize = %d, want %d", defaulted.MessageBatchSize, defaultMessageBatchSize)
	}
	if defaulted.MessageBatchBytes != defaultMessageBatchBytes {
		t.Fatalf("MessageBatchBytes = %d, want %d", defaulted.MessageBatchBytes, defaultMessageBatchBytes)
	}

	explicit := normalizeImportOptions(ImportOptions{
		SubscriberBatchSize: 1,
		MessageBatchSize:    2,
		MessageBatchBytes:   3,
	})
	if explicit.SubscriberBatchSize != 1 || explicit.MessageBatchSize != 2 || explicit.MessageBatchBytes != 3 {
		t.Fatalf("explicit batch options = %+v, want subscriber=1 messages=2 bytes=3", explicit)
	}
}

func TestImportBundleWritesCurrentStores(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	userSlot := testHashSlot("u1", 16)
	channelSlot := testHashSlot("g1", 16)

	writeJSONLFile(t, root, "meta/users.jsonl",
		`{"hash_slot":`+itoa(int(userSlot))+`,"uid":"u1","token":"user-token","device_flag":1,"device_level":2}`,
	)
	writeJSONLFile(t, root, "meta/devices.jsonl",
		`{"hash_slot":`+itoa(int(userSlot))+`,"uid":"u1","device_flag":1,"token":"device-token","device_level":3}`,
	)
	writeJSONLFile(t, root, "meta/channels.jsonl",
		`{"hash_slot":`+itoa(int(channelSlot))+`,"channel_id":"g1","channel_type":2,"ban":0,"disband":0,"send_ban":0,"allow_stranger":1,"large":0,"subscriber_mutation_version":7}`,
	)
	writeJSONLFile(t, root, "meta/subscribers.jsonl",
		`{"hash_slot":`+itoa(int(channelSlot))+`,"channel_id":"g1","channel_type":2,"uid":"u1"}`,
	)
	writeJSONLFile(t, root, "meta/memberships.jsonl",
		`{"hash_slot":`+itoa(int(userSlot))+`,"uid":"u1","channel_id":"g1","channel_type":2,"join_seq":1,"updated_at_ms":1000}`,
	)
	writeJSONLFile(t, root, "meta/conversations.jsonl",
		`{"hash_slot":`+itoa(int(userSlot))+`,"uid":"u1","kind":"normal","channel_id":"g1","channel_type":2,"read_seq":1,"deleted_to_seq":0,"active_at":2000,"updated_at":2001,"sparse_active":true}`,
	)
	writeJSONLFile(t, root, "meta/channel_latest.jsonl",
		`{"hash_slot":`+itoa(int(channelSlot))+`,"channel_id":"g1","channel_type":2,"last_message_id":1001,"last_message_seq":1,"last_at":3000,"from_uid":"u1","client_msg_no":"c1","last_payload_b64":"aGk=","updated_at":3001}`,
	)
	writeJSONLFile(t, root, "message/messages-000001.jsonl",
		`{"channel_key":"g1:2","message_seq":1,"message_id":1001,"client_msg_no":"c1","from_uid":"u1","server_timestamp_ms":3000,"payload_b64":"aGk="}`,
	)
	writeJSONLFile(t, root, "message/channels.jsonl",
		`{"channel_key":"g1:2","channel_id":"g1","channel_type":2}`,
	)
	writeManifestForFiles(t, root, 16, []manifestTestFile{
		{Path: "meta/users.jsonl", Kind: FileKindMetaUsers},
		{Path: "meta/devices.jsonl", Kind: FileKindMetaDevices},
		{Path: "meta/channels.jsonl", Kind: FileKindMetaChannels},
		{Path: "meta/subscribers.jsonl", Kind: FileKindMetaSubscribers},
		{Path: "meta/memberships.jsonl", Kind: FileKindMetaUserChannelMemberships},
		{Path: "meta/conversations.jsonl", Kind: FileKindMetaConversations},
		{Path: "meta/channel_latest.jsonl", Kind: FileKindMetaChannelLatest},
		{Path: "message/messages-000001.jsonl", Kind: FileKindMessageMessages},
		{Path: "message/channels.jsonl", Kind: FileKindMessageChannels},
	})

	store := openImportNodeStore(t)
	stats, err := ImportBundle(ctx, root, store, ImportOptions{HashSlotCount: 16, RequireEmpty: true})
	if err != nil {
		t.Fatalf("ImportBundle(): %v", err)
	}
	if stats.MessagesImported != 1 || stats.SubscribersImported != 1 {
		t.Fatalf("import stats = %+v, want one message and one subscriber", stats)
	}

	user, ok, err := store.Meta().HashSlot(userSlot).GetUser(ctx, "u1")
	if err != nil || !ok {
		t.Fatalf("GetUser() ok=%v err=%v, want ok", ok, err)
	}
	if user.Token != "user-token" || user.DeviceFlag != 1 || user.DeviceLevel != 2 {
		t.Fatalf("user = %+v, want imported token and defaults", user)
	}
	device, ok, err := store.Meta().HashSlot(userSlot).GetDevice(ctx, "u1", 1)
	if err != nil || !ok {
		t.Fatalf("GetDevice() ok=%v err=%v, want ok", ok, err)
	}
	if device.Token != "device-token" || device.DeviceLevel != 3 {
		t.Fatalf("device = %+v, want imported device token", device)
	}
	channel, ok, err := store.Meta().HashSlot(channelSlot).GetChannel(ctx, "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetChannel() ok=%v err=%v, want ok", ok, err)
	}
	if channel.SubscriberCount != 1 {
		t.Fatalf("channel.SubscriberCount = %d, want 1", channel.SubscriberCount)
	}
	conversation, ok, err := store.Meta().HashSlot(userSlot).GetConversationState(ctx, metadb.ConversationKindNormal, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetConversationState() ok=%v err=%v, want ok", ok, err)
	}
	if conversation.ReadSeq != 1 || !conversation.SparseActive {
		t.Fatalf("conversation = %+v, want read seq 1 and sparse active", conversation)
	}
	latest, ok, err := store.Meta().HashSlot(channelSlot).GetChannelLatest(ctx, "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetChannelLatest() ok=%v err=%v, want ok", ok, err)
	}
	if latest.LastMessageSeq != 1 || latest.LastMessageID != 1001 || string(latest.Payload) != "hi" {
		t.Fatalf("latest = %+v, want imported hi payload at seq 1", latest)
	}
	messages, err := store.Messages().Channel("g1:2", msgdb.ChannelID{ID: "g1", Type: 2}).Read(ctx, 1, msgdb.ReadOptions{Limit: 1})
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(messages))
	}
	message := messages[0]
	if string(message.Payload) != "hi" || message.MessageID != 1001 || message.ClientMsgNo != "c1" || message.FromUID != "u1" || message.ServerTimestampMS != 3000 {
		t.Fatalf("message = %+v, want imported message fields", message)
	}
}

func TestImportBundleRejectsNonEmptyTarget(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeManifestForFiles(t, root, 16, nil)

	store := openImportNodeStore(t)
	if err := store.Meta().HashSlot(testHashSlot("u1", 16)).UpsertUser(ctx, metadb.User{UID: "u1", Token: "existing"}); err != nil {
		t.Fatalf("UpsertUser(existing): %v", err)
	}

	_, err := ImportBundle(ctx, root, store, ImportOptions{HashSlotCount: 16, RequireEmpty: true})
	if err == nil || !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("ImportBundle() error = %v, want non-empty target error", err)
	}
}

func TestImportBundleRejectsNonEmptyTargetOutsideBundleHashSlotCount(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeManifestForFiles(t, root, 16, nil)

	store := openImportNodeStore(t)
	if err := store.Meta().HashSlot(20).UpsertUser(ctx, metadb.User{UID: "outside-slot", Token: "existing"}); err != nil {
		t.Fatalf("UpsertUser(existing outside bundle slots): %v", err)
	}

	_, err := ImportBundle(ctx, root, store, ImportOptions{HashSlotCount: 16, RequireEmpty: true})
	if err == nil || !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("ImportBundle() error = %v, want non-empty target error", err)
	}
}

func TestImportBundleRejectsMessageBaseSeqMismatch(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeJSONLFile(t, root, "message/channels.jsonl",
		`{"channel_key":"g1:2","channel_id":"g1","channel_type":2}`,
	)
	writeJSONLFile(t, root, "message/messages-000001.jsonl",
		`{"channel_key":"g1:2","message_seq":1,"message_id":1001,"client_msg_no":"c1","from_uid":"u1","server_timestamp_ms":3000,"payload_b64":"aGk="}`,
	)
	writeManifestForFiles(t, root, 16, []manifestTestFile{
		{Path: "message/channels.jsonl", Kind: FileKindMessageChannels},
		{Path: "message/messages-000001.jsonl", Kind: FileKindMessageMessages},
	})

	store := openImportNodeStore(t)
	log := store.Messages().Channel("g1:2", msgdb.ChannelID{ID: "g1", Type: 2})
	if _, err := log.ApplyFetch(ctx, msgdb.ApplyFetchRequest{
		BaseSeq: 1,
		Records: []msgdb.Record{
			{ID: 9001, Payload: []byte("existing")},
		},
	}); err != nil {
		t.Fatalf("ApplyFetch(existing): %v", err)
	}

	_, err := ImportBundle(ctx, root, store, ImportOptions{HashSlotCount: 16})
	if err == nil {
		t.Fatal("ImportBundle() error = nil, want base sequence mismatch")
	}
	messages, readErr := log.Read(ctx, 1, msgdb.ReadOptions{Limit: 2})
	if readErr != nil {
		t.Fatalf("Read(after failed import): %v", readErr)
	}
	if len(messages) != 1 || messages[0].MessageID != 9001 {
		t.Fatalf("messages after failed import = %+v, want only existing message", messages)
	}
}

func openImportNodeStore(t *testing.T) *db.NodeStore {
	t.Helper()
	store, err := db.OpenNodeStore(db.DefaultNodeStoreOptions(t.TempDir()))
	if err != nil {
		t.Fatalf("OpenNodeStore(): %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close NodeStore: %v", err)
		}
	})
	return store
}

func testHashSlot(key string, count uint16) uint16 {
	return cluster.HashSlotForKey(key, count)
}
