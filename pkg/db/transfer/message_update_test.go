package transfer

import (
	"context"
	"encoding/json"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"path/filepath"
	"strings"
	"testing"
)

func TestMessageUpdateOfflineTransferPreservesEveryProjection(t *testing.T) {
	ctx := context.Background()
	const slots = 16
	source, sourceOpts := seedVerifyNodeStore(t, slots, verifySeedOptions{})
	hs := testHashSlot("g1", slots)
	// Values above JavaScript's exact-number boundary must survive JSONL unchanged.
	version := uint64(9007199254740993)
	latest := metadb.MessageUpdate{ChannelID: "g1", ChannelType: 2, MessageID: 1001, MessageSeq: 1, Version: version, UpdateSeq: version, Payload: []byte("latest"), UpdatedAtMS: 123}
	pending := latest
	pending.Payload = nil
	pending.Pending = 1
	pending.PendingAfterUID = "z"
	records := []struct {
		table string
		row   any
	}{
		{"message_update_head", metadb.MessageUpdateHead{ChannelID: "g1", ChannelType: 2, Generation: "incarnation", UpdateSeq: version, ReplicaSet: "[1,2,3]"}},
		{"message_update", latest},
		{"message_update_request", metadb.MessageUpdateRequest{ChannelID: "g1", ChannelType: 2, MessageID: 1001, MessageSeq: 1, RequestID: "request", Digest: strings.Repeat("a", 64), Version: version, UpdateSeq: version, UpdatedAtMS: 123}},
		{"message_update_pending", pending},
	}
	for _, record := range records {
		data, err := json.Marshal(record.row)
		if err != nil {
			t.Fatal(err)
		}
		if err = source.Meta().ImportMessageUpdate(ctx, hs, metadb.MessageUpdateImport{Table: record.table, Row: data}); err != nil {
			t.Fatalf("seed %s: %v", record.table, err)
		}
	}
	closeVerifyNodeStore(t, source)
	original := openVerifyInspectStore(t, sourceOpts, slots)
	root := filepath.Join(t.TempDir(), "bundle")
	if _, err := ExportBundle(ctx, root, original, ExportOptions{HashSlotCount: slots, PageSize: 1}); err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if countManifestKind(manifest, FileKindMetaMessageUpdates) != 4 {
		t.Fatal("edit projection omitted from manifest")
	}
	if _, err = ValidateBundle(ctx, root, ImportOptions{HashSlotCount: slots}); err != nil {
		t.Fatal(err)
	}
	target, targetOpts := openExportNodeStore(t, t.TempDir())
	if _, err = ImportBundle(ctx, root, target, ImportOptions{HashSlotCount: slots, RequireEmpty: true}); err != nil {
		t.Fatal(err)
	}
	page, err := metadb.InspectScan(ctx, target.Meta(), metadb.InspectScanRequest{Table: "message_update", HashSlot: hs, HashSlotSet: true, Limit: 10})
	if err != nil || len(page.Rows) != 1 || page.Rows[0]["version"] != version || string(page.Rows[0]["payload"].([]byte)) != "latest" {
		t.Fatalf("imported=%+v err=%v", page, err)
	}
	closeVerifyNodeStore(t, target)
	restored := openVerifyInspectStore(t, targetOpts, slots)
	report, err := VerifyStores(ctx, original, restored, VerifyOptions{HashSlotCount: slots})
	if err != nil || !report.Equal {
		t.Fatalf("verify=%+v err=%v", report, err)
	}
}

func TestMessageUpdateImportRejectsUnknownOrInconsistentProjection(t *testing.T) {
	for _, data := range []string{`{"hash_slot":1,"table":"other","row":{}}`, `{"hash_slot":1,"table":"message_update","row":{"channel_id":"g","channel_type":2,"message_id":1,"version":1,"update_seq":1,"message_seq":1,"payload":"eA==","pending":1}}`} {
		if _, err := decodeRecord(FileKindMetaMessageUpdates, []byte(data)); err == nil {
			t.Fatal("invalid edit import accepted")
		}
	}
	target, _ := openExportNodeStore(t, t.TempDir())
	data, _ := json.Marshal(metadb.MessageUpdate{ChannelID: "g", ChannelType: 2, MessageID: 1, MessageSeq: 1, Version: 1, UpdateSeq: 1, Payload: []byte("x")})
	if err := target.Meta().ImportMessageUpdate(context.Background(), 0, metadb.MessageUpdateImport{Table: "message_update", Row: data}); err == nil {
		t.Fatal("orphan replacement imported without its head")
	}
}
