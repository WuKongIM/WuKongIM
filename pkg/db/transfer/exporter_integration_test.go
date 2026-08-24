//go:build integration

package transfer

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db"
	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	msgdb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func TestExportBundleRoundTripsCurrentStores(t *testing.T) {
	ctx := context.Background()
	const hashSlotCount uint16 = 16
	userSlot := testHashSlot("u1", hashSlotCount)
	channelSlot := testHashSlot("g1", hashSlotCount)
	personChannelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	personChannelSlot := testHashSlot(personChannelID, hashSlotCount)

	seedStore, seedOptions := openExportNodeStore(t, t.TempDir())
	if err := seedStore.Meta().HashSlot(userSlot).UpsertUser(ctx, metadb.User{
		UID:         "u1",
		Token:       "user-token",
		DeviceFlag:  1,
		DeviceLevel: 2,
	}); err != nil {
		t.Fatalf("UpsertUser(): %v", err)
	}
	if err := seedStore.Meta().HashSlot(userSlot).UpsertDevice(ctx, metadb.Device{
		UID:         "u1",
		DeviceFlag:  1,
		Token:       "device-token",
		DeviceLevel: 3,
	}); err != nil {
		t.Fatalf("UpsertDevice(): %v", err)
	}
	if err := seedStore.Meta().HashSlot(channelSlot).UpsertChannel(ctx, metadb.Channel{
		ChannelID:                 "g1",
		ChannelType:               2,
		AllowStranger:             1,
		Large:                     1,
		SubscriberMutationVersion: 7,
	}); err != nil {
		t.Fatalf("UpsertChannel(): %v", err)
	}
	if err := seedStore.Meta().HashSlot(channelSlot).AddSubscribers(ctx, "g1", 2, []string{"u1"}, 0); err != nil {
		t.Fatalf("AddSubscribers(): %v", err)
	}
	if err := seedStore.Meta().HashSlot(userSlot).UpsertUserChannelMembership(ctx, metadb.UserChannelMembership{
		UID:           "u1",
		ChannelID:     "g1",
		ChannelType:   2,
		JoinSeq:       1,
		ReadSeq:       1,
		ActivatedAt:   2000,
		SourceVersion: 7,
		UpdatedAt:     2001,
	}); err != nil {
		t.Fatalf("UpsertUserChannelMembership(): %v", err)
	}
	if err := seedStore.Meta().HashSlot(userSlot).UpsertUserCMDChannelMembership(ctx, metadb.UserCMDChannelMembership{
		UID: "u1", CommandChannelID: "g1____cmd", ChannelType: 2,
		StartSeq: 3, AckSeq: 4, UpdatedAt: 2002,
	}); err != nil {
		t.Fatalf("UpsertUserCMDChannelMembership(): %v", err)
	}
	if err := seedStore.Meta().HashSlot(channelSlot).UpsertChannelLatest(ctx, metadb.ChannelLatest{
		ChannelID:      "g1",
		ChannelType:    2,
		LastMessageID:  1002,
		LastMessageSeq: 2,
		LastAt:         3001,
		FromUID:        "u2",
		ClientMsgNo:    "c2",
		Payload:        []byte("payload-two"),
		UpdatedAt:      3002,
	}); err != nil {
		t.Fatalf("UpsertChannelLatest(): %v", err)
	}
	batch := seedStore.Meta().NewBatch()
	if err := batch.EnsurePersonDirectoryTask(personChannelSlot, metadb.PersonDirectoryTask{
		ChannelID: personChannelID, ChannelType: 1, CommittedTail: 9, CreatedAt: 123, Generation: 1,
	}); err != nil {
		t.Fatalf("EnsurePersonDirectoryTask(): %v", err)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit(person-directory task): %v", err)
	}
	if err := batch.Close(); err != nil {
		t.Fatalf("Close(person-directory task batch): %v", err)
	}
	log, err := seedStore.Messages().Channel("g1:2", msgdb.ChannelID{ID: "g1", Type: 2})
	if err != nil {
		t.Fatalf("Channel(): %v", err)
	}
	if _, err := log.Append(ctx, []msgdb.Record{
		{ID: 1001, ClientMsgNo: "c1", FromUID: "u1", Payload: []byte("payload-one"), ServerTimestampMS: 3000},
		{ID: 1002, ClientMsgNo: "c2", FromUID: "u2", Payload: []byte("payload-two"), ServerTimestampMS: 3001},
	}, msgdb.AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close message log: %v", err)
	}
	if err := seedStore.Close(); err != nil {
		t.Fatalf("Close seed store: %v", err)
	}

	readStore, err := inspect.OpenStore(inspect.Options{
		MetaPath:      seedOptions.MetaPath,
		MessagePath:   seedOptions.MessagePath,
		HashSlotCount: hashSlotCount,
		DefaultLimit:  100,
		MaxLimit:      10000,
	})
	if err != nil {
		t.Fatalf("OpenStore(): %v", err)
	}
	defer readStore.Close()

	exportRoot := filepath.Join(t.TempDir(), "bundle")
	stats, err := ExportBundle(ctx, exportRoot, readStore, ExportOptions{
		HashSlotCount:   hashSlotCount,
		PageSize:        1,
		MessageFileRows: 1,
	})
	if err != nil {
		t.Fatalf("ExportBundle(): %v", err)
	}
	if stats.RowsExported == 0 || stats.MessagesExported != 2 || stats.FilesWritten == 0 {
		t.Fatalf("export stats = %+v, want rows, two messages, and files", stats)
	}
	if _, err := ValidateBundle(ctx, exportRoot, ImportOptions{HashSlotCount: hashSlotCount}); err != nil {
		t.Fatalf("ValidateBundle(exported): %v", err)
	}
	manifest, err := LoadManifest(exportRoot)
	if err != nil {
		t.Fatalf("LoadManifest(exported): %v", err)
	}
	if got := countManifestKind(manifest, FileKindMessageMessages); got != 2 {
		t.Fatalf("message file count = %d, want 2", got)
	}
	if got := countManifestKind(manifest, FileKindMetaPersonDirectoryTasks); got != 1 {
		t.Fatalf("person-directory task file count = %d, want 1", got)
	}

	target := openImportNodeStore(t)
	if _, err := ImportBundle(ctx, exportRoot, target, ImportOptions{HashSlotCount: hashSlotCount, RequireEmpty: true, MessageBatchSize: 1}); err != nil {
		t.Fatalf("ImportBundle(exported): %v", err)
	}
	user, ok, err := target.Meta().HashSlot(userSlot).GetUser(ctx, "u1")
	if err != nil || !ok {
		t.Fatalf("GetUser() ok=%v err=%v, want ok", ok, err)
	}
	if user.Token != "user-token" || user.DeviceLevel != 2 {
		t.Fatalf("user = %+v, want exported user fields", user)
	}
	channel, ok, err := target.Meta().HashSlot(channelSlot).GetChannel(ctx, "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetChannel() ok=%v err=%v, want ok", ok, err)
	}
	if channel.SubscriberCount != 1 || channel.Large != 1 {
		t.Fatalf("channel = %+v, want subscriber count and large flag", channel)
	}
	personChannel, ok, err := target.Meta().HashSlot(personChannelSlot).GetChannel(ctx, personChannelID, 1)
	if err != nil || !ok || personChannel.DirectoryProjectionState != metadb.DirectoryProjectionPending {
		t.Fatalf("person channel = %+v ok=%v err=%v, want pending projection", personChannel, ok, err)
	}
	personTask, ok, err := target.Meta().HashSlot(personChannelSlot).GetPersonDirectoryTask(ctx, personChannelID, 1)
	if err != nil || !ok || personTask.CommittedTail != 9 || personTask.CreatedAt != 123 {
		t.Fatalf("person-directory task = %+v ok=%v err=%v, want preserved durable task", personTask, ok, err)
	}
	membership, ok, err := target.Meta().HashSlot(userSlot).GetUserChannelMembership(ctx, "u1", "g1", 2)
	if err != nil || !ok || membership.ReadSeq != 1 || membership.ActivatedAt != 2000 || membership.SourceVersion != 7 {
		t.Fatalf("membership = %+v ok=%v err=%v", membership, ok, err)
	}
	cmdMembership, ok, err := target.Meta().HashSlot(userSlot).GetUserCMDChannelMembership(ctx, "u1", "g1____cmd", 2)
	if err != nil || !ok || cmdMembership.StartSeq != 3 || cmdMembership.AckSeq != 4 {
		t.Fatalf("CMD membership = %+v ok=%v err=%v", cmdMembership, ok, err)
	}
	latest, ok, err := target.Meta().HashSlot(channelSlot).GetChannelLatest(ctx, "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetChannelLatest() ok=%v err=%v, want ok", ok, err)
	}
	if latest.LastMessageSeq != 2 || string(latest.Payload) != "payload-two" {
		t.Fatalf("latest = %+v, want second message projection", latest)
	}
	messages := readImportMessages(t, ctx, target.Messages(), "g1:2", msgdb.ChannelID{ID: "g1", Type: 2}, 1, 2)
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2: %+v", len(messages), messages)
	}
	if messages[0].ServerTimestampMS != 3000 || messages[1].ServerTimestampMS != 3001 {
		t.Fatalf("message timestamps = %d,%d, want 3000,3001", messages[0].ServerTimestampMS, messages[1].ServerTimestampMS)
	}
	if string(messages[0].Payload) != "payload-one" || string(messages[1].Payload) != "payload-two" {
		t.Fatalf("message payloads = %q,%q, want exported payloads", messages[0].Payload, messages[1].Payload)
	}
}

func openExportNodeStore(t *testing.T, basePath string) (*db.NodeStore, db.NodeStoreOptions) {
	t.Helper()
	opts := db.DefaultNodeStoreOptions(basePath)
	store, err := db.OpenNodeStore(opts)
	if err != nil {
		t.Fatalf("OpenNodeStore(): %v", err)
	}
	t.Cleanup(func() {
		if !store.Closed() {
			if err := store.Close(); err != nil {
				t.Fatalf("Close NodeStore: %v", err)
			}
		}
	})
	return store, opts
}

func countManifestKind(manifest Manifest, kind FileKind) int {
	count := 0
	for _, entry := range manifest.Files {
		if entry.Kind == kind {
			count++
		}
	}
	return count
}
