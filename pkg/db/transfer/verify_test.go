package transfer

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db"
	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	msgdb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestVerifyStoresReportsEqualForRoundTripStores(t *testing.T) {
	ctx := context.Background()
	const hashSlotCount uint16 = 16

	source, sourceOpts := seedVerifyNodeStore(t, hashSlotCount, verifySeedOptions{})
	closeVerifyNodeStore(t, source)

	exportInspect := openVerifyInspectStore(t, sourceOpts, hashSlotCount)
	exportRoot := filepath.Join(t.TempDir(), "bundle")
	if _, err := ExportBundle(ctx, exportRoot, exportInspect, ExportOptions{
		HashSlotCount:   hashSlotCount,
		PageSize:        1,
		MessageFileRows: 1,
	}); err != nil {
		t.Fatalf("ExportBundle(): %v", err)
	}
	closeVerifyInspectStore(t, exportInspect)

	target := openImportNodeStore(t)
	if _, err := ImportBundle(ctx, exportRoot, target, ImportOptions{HashSlotCount: hashSlotCount, RequireEmpty: true}); err != nil {
		t.Fatalf("ImportBundle(): %v", err)
	}
	targetOpts := target.Options()
	closeVerifyNodeStore(t, target)

	report, err := VerifyStores(ctx, openVerifyInspectStore(t, sourceOpts, hashSlotCount), openVerifyInspectStore(t, targetOpts, hashSlotCount), VerifyOptions{
		HashSlotCount: hashSlotCount,
		PageSize:      1,
		Mode:          VerifyModeFull,
	})
	if err != nil {
		t.Fatalf("VerifyStores(): %v", err)
	}
	if !report.Equal {
		t.Fatalf("report.Equal = false: %+v", report)
	}
	if report.Mode != VerifyModeFull {
		t.Fatalf("report.Mode = %q, want %q", report.Mode, VerifyModeFull)
	}
	if len(report.Meta) != 7 {
		t.Fatalf("len(report.Meta) = %d, want 7: %+v", len(report.Meta), report.Meta)
	}
	if len(report.Message) != 2 {
		t.Fatalf("len(report.Message) = %d, want 2: %+v", len(report.Message), report.Message)
	}
	if len(report.Mismatches) != 0 {
		t.Fatalf("mismatches = %+v, want none", report.Mismatches)
	}
}

func TestVerifyStoresReportsMetaMismatch(t *testing.T) {
	ctx := context.Background()
	const hashSlotCount uint16 = 16

	source, sourceOpts := seedVerifyNodeStore(t, hashSlotCount, verifySeedOptions{})
	closeVerifyNodeStore(t, source)
	target, targetOpts := seedVerifyNodeStore(t, hashSlotCount, verifySeedOptions{UserToken: "changed-token"})
	closeVerifyNodeStore(t, target)

	report, err := VerifyStores(ctx, openVerifyInspectStore(t, sourceOpts, hashSlotCount), openVerifyInspectStore(t, targetOpts, hashSlotCount), VerifyOptions{
		HashSlotCount: hashSlotCount,
		PageSize:      1,
		Mode:          VerifyModeSummary,
	})
	if err != nil {
		t.Fatalf("VerifyStores(): %v", err)
	}
	if report.Equal {
		t.Fatalf("report.Equal = true, want false: %+v", report)
	}
	if !verifyReportHasMismatch(report, "meta.users") {
		t.Fatalf("mismatches = %+v, want meta.users mismatch", report.Mismatches)
	}
}

func TestVerifyStoresReportsMessageMismatch(t *testing.T) {
	ctx := context.Background()
	const hashSlotCount uint16 = 16

	source, sourceOpts := seedVerifyNodeStore(t, hashSlotCount, verifySeedOptions{})
	closeVerifyNodeStore(t, source)
	target, targetOpts := seedVerifyNodeStore(t, hashSlotCount, verifySeedOptions{SecondPayload: "changed-payload"})
	closeVerifyNodeStore(t, target)

	report, err := VerifyStores(ctx, openVerifyInspectStore(t, sourceOpts, hashSlotCount), openVerifyInspectStore(t, targetOpts, hashSlotCount), VerifyOptions{
		HashSlotCount: hashSlotCount,
		PageSize:      1,
		Mode:          VerifyModeSummary,
	})
	if err != nil {
		t.Fatalf("VerifyStores(): %v", err)
	}
	if report.Equal {
		t.Fatalf("report.Equal = true, want false: %+v", report)
	}
	if !verifyReportHasMismatch(report, "message.messages") {
		t.Fatalf("mismatches = %+v, want message.messages mismatch", report.Mismatches)
	}
}

func TestVerifyStoresRejectsUnknownMode(t *testing.T) {
	_, err := VerifyStores(context.Background(), nil, nil, VerifyOptions{
		HashSlotCount: 16,
		Mode:          VerifyMode("bad"),
	})
	if err == nil || !errors.Is(err, ErrValidation) {
		t.Fatalf("VerifyStores() error = %v, want validation error", err)
	}
}

type verifySeedOptions struct {
	UserToken     string
	SecondPayload string
}

func seedVerifyNodeStore(t *testing.T, hashSlotCount uint16, seedOpts verifySeedOptions) (*db.NodeStore, db.NodeStoreOptions) {
	t.Helper()
	if seedOpts.UserToken == "" {
		seedOpts.UserToken = "user-token"
	}
	if seedOpts.SecondPayload == "" {
		seedOpts.SecondPayload = "payload-two"
	}

	ctx := context.Background()
	userSlot := testHashSlot("u1", hashSlotCount)
	channelSlot := testHashSlot("g1", hashSlotCount)
	store, opts := openExportNodeStore(t, t.TempDir())
	if err := store.Meta().HashSlot(userSlot).UpsertUser(ctx, metadb.User{
		UID:         "u1",
		Token:       seedOpts.UserToken,
		DeviceFlag:  1,
		DeviceLevel: 2,
	}); err != nil {
		t.Fatalf("UpsertUser(): %v", err)
	}
	if err := store.Meta().HashSlot(userSlot).UpsertDevice(ctx, metadb.Device{
		UID:         "u1",
		DeviceFlag:  1,
		Token:       "device-token",
		DeviceLevel: 3,
	}); err != nil {
		t.Fatalf("UpsertDevice(): %v", err)
	}
	if err := store.Meta().HashSlot(channelSlot).UpsertChannel(ctx, metadb.Channel{
		ChannelID:                 "g1",
		ChannelType:               2,
		AllowStranger:             1,
		Large:                     1,
		SubscriberMutationVersion: 7,
	}); err != nil {
		t.Fatalf("UpsertChannel(): %v", err)
	}
	if err := store.Meta().HashSlot(channelSlot).AddSubscribers(ctx, "g1", 2, []string{"u1"}, 0); err != nil {
		t.Fatalf("AddSubscribers(): %v", err)
	}
	if err := store.Meta().HashSlot(userSlot).UpsertUserChannelMembership(ctx, metadb.UserChannelMembership{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		JoinSeq:     1,
		UpdatedAt:   1000,
	}); err != nil {
		t.Fatalf("UpsertUserChannelMembership(): %v", err)
	}
	if err := store.Meta().HashSlot(userSlot).UpsertConversationState(ctx, metadb.ConversationState{
		UID:          "u1",
		Kind:         metadb.ConversationKindNormal,
		ChannelID:    "g1",
		ChannelType:  2,
		ReadSeq:      1,
		DeletedToSeq: 0,
		ActiveAt:     2000,
		UpdatedAt:    2001,
		SparseActive: true,
	}); err != nil {
		t.Fatalf("UpsertConversationState(): %v", err)
	}
	if err := store.Meta().HashSlot(channelSlot).UpsertChannelLatest(ctx, metadb.ChannelLatest{
		ChannelID:      "g1",
		ChannelType:    2,
		LastMessageID:  1002,
		LastMessageSeq: 2,
		LastAt:         3001,
		FromUID:        "u2",
		ClientMsgNo:    "c2",
		Payload:        []byte(seedOpts.SecondPayload),
		UpdatedAt:      3002,
	}); err != nil {
		t.Fatalf("UpsertChannelLatest(): %v", err)
	}
	log := store.Messages().Channel("g1:2", msgdb.ChannelID{ID: "g1", Type: 2})
	if _, err := log.Append(ctx, []msgdb.Record{
		{ID: 1001, ClientMsgNo: "c1", FromUID: "u1", Payload: []byte("payload-one"), ServerTimestampMS: 3000},
		{ID: 1002, ClientMsgNo: "c2", FromUID: "u2", Payload: []byte(seedOpts.SecondPayload), ServerTimestampMS: 3001},
	}, msgdb.AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	return store, opts
}

func openVerifyInspectStore(t *testing.T, opts db.NodeStoreOptions, hashSlotCount uint16) *inspect.Store {
	t.Helper()
	store, err := inspect.OpenStore(inspect.Options{
		MetaPath:      opts.MetaPath,
		MessagePath:   opts.MessagePath,
		HashSlotCount: hashSlotCount,
		DefaultLimit:  100,
		MaxLimit:      10000,
	})
	if err != nil {
		t.Fatalf("OpenStore(): %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close inspect store: %v", err)
		}
	})
	return store
}

func closeVerifyNodeStore(t *testing.T, store *db.NodeStore) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Fatalf("Close NodeStore: %v", err)
	}
}

func closeVerifyInspectStore(t *testing.T, store *inspect.Store) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Fatalf("Close inspect store: %v", err)
	}
}

func verifyReportHasMismatch(report VerifyReport, scope string) bool {
	for _, mismatch := range report.Mismatches {
		if mismatch.Scope == scope {
			return true
		}
	}
	return false
}
