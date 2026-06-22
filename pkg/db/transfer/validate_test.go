package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestValidateBundleRejectsHashSlotMismatch(t *testing.T) {
	root := t.TempDir()
	uid := "u1"
	wrong := cluster.HashSlotForKey(uid, 16) + 1
	if wrong >= 16 {
		wrong = 0
	}
	writeJSONLFile(t, root, "meta/users.jsonl", `{"hash_slot":`+itoa(int(wrong))+`,"uid":"`+uid+`"}`)
	writeManifestForFiles(t, root, 16, []manifestTestFile{{Path: "meta/users.jsonl", Kind: FileKindMetaUsers}})

	_, err := ValidateBundle(context.Background(), root, ImportOptions{HashSlotCount: 16})
	if err == nil || !strings.Contains(err.Error(), "hash_slot") {
		t.Fatalf("ValidateBundle() error = %v, want hash_slot mismatch", err)
	}
}

func TestValidateBundleRejectsUnsortedSubscribers(t *testing.T) {
	root := t.TempDir()
	writeJSONLFile(t, root, "meta/subscribers.jsonl",
		`{"hash_slot":`+itoa(int(cluster.HashSlotForKey("b", 16)))+`,"channel_id":"b","channel_type":2,"uid":"u1"}`,
		`{"hash_slot":`+itoa(int(cluster.HashSlotForKey("a", 16)))+`,"channel_id":"a","channel_type":2,"uid":"u2"}`,
	)
	writeManifestForFiles(t, root, 16, []manifestTestFile{{Path: "meta/subscribers.jsonl", Kind: FileKindMetaSubscribers}})

	_, err := ValidateBundle(context.Background(), root, ImportOptions{HashSlotCount: 16})
	if err == nil || !strings.Contains(err.Error(), "subscriber order") {
		t.Fatalf("ValidateBundle() error = %v, want subscriber order error", err)
	}
}

func TestValidateBundleRejectsSubscriberHashSlotBeforeOrder(t *testing.T) {
	root := t.TempDir()
	writeJSONLFile(t, root, "meta/subscribers.jsonl",
		`{"hash_slot":15,"channel_id":"b","channel_type":2,"uid":"u1"}`,
		`{"hash_slot":`+itoa(int(cluster.HashSlotForKey("a", 16)))+`,"channel_id":"a","channel_type":2,"uid":"u2"}`,
	)
	writeManifestForFiles(t, root, 16, []manifestTestFile{{Path: "meta/subscribers.jsonl", Kind: FileKindMetaSubscribers}})

	_, err := ValidateBundle(context.Background(), root, ImportOptions{HashSlotCount: 16})
	if err == nil || !strings.Contains(err.Error(), "hash_slot") {
		t.Fatalf("ValidateBundle() error = %v, want immediate hash_slot mismatch", err)
	}
}

func TestValidateBundleRejectsMessageSequenceGap(t *testing.T) {
	root := t.TempDir()
	writeJSONLFile(t, root, "message/channels.jsonl", `{"channel_key":"g1:2","channel_id":"g1","channel_type":2}`)
	writeJSONLFile(t, root, "message/messages-000001.jsonl",
		`{"channel_key":"g1:2","message_seq":1,"message_id":1001,"payload_b64":""}`,
		`{"channel_key":"g1:2","message_seq":3,"message_id":1003,"payload_b64":""}`,
	)
	writeManifestForFiles(t, root, 16, []manifestTestFile{
		{Path: "message/channels.jsonl", Kind: FileKindMessageChannels},
		{Path: "message/messages-000001.jsonl", Kind: FileKindMessageMessages},
	})

	_, err := ValidateBundle(context.Background(), root, ImportOptions{HashSlotCount: 16})
	if err == nil || !strings.Contains(err.Error(), "contiguous") {
		t.Fatalf("ValidateBundle() error = %v, want contiguous error", err)
	}
}

func TestValidateBundleRejectsDuplicateMessageID(t *testing.T) {
	root := t.TempDir()
	writeJSONLFile(t, root, "message/channels.jsonl", `{"channel_key":"g1:2","channel_id":"g1","channel_type":2}`)
	writeJSONLFile(t, root, "message/messages-000001.jsonl",
		`{"channel_key":"g1:2","message_seq":1,"message_id":1001,"payload_b64":""}`,
		`{"channel_key":"g1:2","message_seq":2,"message_id":1001,"payload_b64":""}`,
	)
	writeManifestForFiles(t, root, 16, []manifestTestFile{
		{Path: "message/channels.jsonl", Kind: FileKindMessageChannels},
		{Path: "message/messages-000001.jsonl", Kind: FileKindMessageMessages},
	})

	_, err := ValidateBundle(context.Background(), root, ImportOptions{HashSlotCount: 16})
	if err == nil || !strings.Contains(err.Error(), "duplicate message_id") {
		t.Fatalf("ValidateBundle() error = %v, want duplicate message_id", err)
	}
}

func TestValidateBundleRejectsDuplicateIdempotencyKey(t *testing.T) {
	root := t.TempDir()
	writeJSONLFile(t, root, "message/channels.jsonl", `{"channel_key":"g1:2","channel_id":"g1","channel_type":2}`)
	writeJSONLFile(t, root, "message/messages-000001.jsonl",
		`{"channel_key":"g1:2","message_seq":1,"message_id":1001,"from_uid":"u1","client_msg_no":"c1","payload_b64":""}`,
		`{"channel_key":"g1:2","message_seq":2,"message_id":1002,"from_uid":"u1","client_msg_no":"c1","payload_b64":""}`,
	)
	writeManifestForFiles(t, root, 16, []manifestTestFile{
		{Path: "message/channels.jsonl", Kind: FileKindMessageChannels},
		{Path: "message/messages-000001.jsonl", Kind: FileKindMessageMessages},
	})

	_, err := ValidateBundle(context.Background(), root, ImportOptions{HashSlotCount: 16})
	if err == nil || !strings.Contains(err.Error(), "duplicate idempotency") {
		t.Fatalf("ValidateBundle() error = %v, want duplicate idempotency", err)
	}
}

func TestValidateBundleRejectsMissingMessageChannel(t *testing.T) {
	root := t.TempDir()
	writeJSONLFile(t, root, "message/messages-000001.jsonl",
		`{"channel_key":"g1:2","message_seq":1,"message_id":1001,"payload_b64":""}`,
	)
	writeManifestForFiles(t, root, 16, []manifestTestFile{
		{Path: "message/messages-000001.jsonl", Kind: FileKindMessageMessages},
	})

	_, err := ValidateBundle(context.Background(), root, ImportOptions{HashSlotCount: 16})
	if err == nil || !strings.Contains(err.Error(), "missing message channel") {
		t.Fatalf("ValidateBundle() error = %v, want missing message channel", err)
	}
}

func TestValidateBundleRejectsMissingMessageChannelWithSortedCatalog(t *testing.T) {
	root := t.TempDir()
	writeJSONLFile(t, root, "message/channels.jsonl",
		`{"channel_key":"a:2","channel_id":"a","channel_type":2}`,
		`{"channel_key":"c:2","channel_id":"c","channel_type":2}`,
	)
	writeJSONLFile(t, root, "message/messages-000001.jsonl",
		`{"channel_key":"b:2","message_seq":1,"message_id":1001,"payload_b64":""}`,
	)
	writeManifestForFiles(t, root, 16, []manifestTestFile{
		{Path: "message/channels.jsonl", Kind: FileKindMessageChannels},
		{Path: "message/messages-000001.jsonl", Kind: FileKindMessageMessages},
	})

	_, err := ValidateBundle(context.Background(), root, ImportOptions{HashSlotCount: 16})
	if err == nil || !strings.Contains(err.Error(), "missing message channel") {
		t.Fatalf("ValidateBundle() error = %v, want missing message channel", err)
	}
}

func TestValidateBundleRejectsUnsortedMessageChannels(t *testing.T) {
	root := t.TempDir()
	writeJSONLFile(t, root, "message/channels.jsonl",
		`{"channel_key":"g2:2","channel_id":"g2","channel_type":2}`,
		`{"channel_key":"g1:2","channel_id":"g1","channel_type":2}`,
	)
	writeManifestForFiles(t, root, 16, []manifestTestFile{
		{Path: "message/channels.jsonl", Kind: FileKindMessageChannels},
	})

	_, err := ValidateBundle(context.Background(), root, ImportOptions{HashSlotCount: 16})
	if err == nil || !strings.Contains(err.Error(), "message channel order") {
		t.Fatalf("ValidateBundle() error = %v, want message channel order error", err)
	}
}

func TestValidateBundleAcceptsMessagesListedBeforeChannels(t *testing.T) {
	root := t.TempDir()
	writeJSONLFile(t, root, "message/messages-000001.jsonl",
		`{"channel_key":"g1:2","message_seq":1,"message_id":1001,"payload_b64":""}`,
	)
	writeJSONLFile(t, root, "message/channels.jsonl", `{"channel_key":"g1:2","channel_id":"g1","channel_type":2}`)
	writeManifestForFiles(t, root, 16, []manifestTestFile{
		{Path: "message/messages-000001.jsonl", Kind: FileKindMessageMessages},
		{Path: "message/channels.jsonl", Kind: FileKindMessageChannels},
	})

	if _, err := ValidateBundle(context.Background(), root, ImportOptions{HashSlotCount: 16}); err != nil {
		t.Fatalf("ValidateBundle() error = %v", err)
	}
}

func TestValidateBundleRejectsHashSlotCountMismatch(t *testing.T) {
	root := t.TempDir()
	writeManifestForFiles(t, root, 16, nil)

	_, err := ValidateBundle(context.Background(), root, ImportOptions{HashSlotCount: 32})
	if err == nil || !strings.Contains(err.Error(), "hash_slot_count") {
		t.Fatalf("ValidateBundle() error = %v, want hash_slot_count mismatch", err)
	}
}

func TestValidateBundleRejectsManifestRowCountTooFew(t *testing.T) {
	root := t.TempDir()
	uid := "u1"
	writeJSONLFile(t, root, "meta/users.jsonl", `{"hash_slot":`+itoa(int(cluster.HashSlotForKey(uid, 16)))+`,"uid":"`+uid+`"}`)
	writeManifestForFilesWithRows(t, root, 16, []manifestTestFile{
		{Path: "meta/users.jsonl", Kind: FileKindMetaUsers, Rows: 2},
	})

	_, err := ValidateBundle(context.Background(), root, ImportOptions{HashSlotCount: 16})
	if err == nil || !strings.Contains(err.Error(), "row count") {
		t.Fatalf("ValidateBundle() error = %v, want row count error", err)
	}
}

func TestValidateBundleRejectsManifestRowCountTooMany(t *testing.T) {
	root := t.TempDir()
	uid1 := "u1"
	uid2 := "u2"
	writeJSONLFile(t, root, "meta/users.jsonl",
		`{"hash_slot":`+itoa(int(cluster.HashSlotForKey(uid1, 16)))+`,"uid":"`+uid1+`"}`,
		`{"hash_slot":`+itoa(int(cluster.HashSlotForKey(uid2, 16)))+`,"uid":"`+uid2+`"}`,
	)
	writeManifestForFilesWithRows(t, root, 16, []manifestTestFile{
		{Path: "meta/users.jsonl", Kind: FileKindMetaUsers, Rows: 1},
	})

	_, err := ValidateBundle(context.Background(), root, ImportOptions{HashSlotCount: 16})
	if err == nil || !strings.Contains(err.Error(), "row count") {
		t.Fatalf("ValidateBundle() error = %v, want row count error", err)
	}
}

func TestValidateBundleReportsStats(t *testing.T) {
	root := t.TempDir()
	uid := "u1"
	writeJSONLFile(t, root, "meta/users.jsonl", `{"hash_slot":`+itoa(int(cluster.HashSlotForKey(uid, 16)))+`,"uid":"`+uid+`"}`)
	writeJSONLFile(t, root, "message/channels.jsonl", `{"channel_key":"g1:2","channel_id":"g1","channel_type":2}`)
	writeJSONLFile(t, root, "message/messages-000001.jsonl",
		`{"channel_key":"g1:2","message_seq":1,"message_id":1001,"from_uid":"u1","client_msg_no":"c1","payload_b64":""}`,
		`{"channel_key":"g1:2","message_seq":2,"message_id":1002,"from_uid":"u1","client_msg_no":"c2","payload_b64":""}`,
	)
	files := []manifestTestFile{
		{Path: "meta/users.jsonl", Kind: FileKindMetaUsers},
		{Path: "message/channels.jsonl", Kind: FileKindMessageChannels},
		{Path: "message/messages-000001.jsonl", Kind: FileKindMessageMessages},
	}
	writeManifestForFiles(t, root, 16, files)

	stats, err := ValidateBundle(context.Background(), root, ImportOptions{})
	if err != nil {
		t.Fatalf("ValidateBundle() error = %v", err)
	}
	if stats.Files != int64(len(files)) {
		t.Fatalf("Files = %d, want %d", stats.Files, len(files))
	}
	if stats.RowsValidated != 4 {
		t.Fatalf("RowsValidated = %d, want 4", stats.RowsValidated)
	}
	if want := bundleFilesSize(t, root, files); stats.BytesRead != want {
		t.Fatalf("BytesRead = %d, want %d", stats.BytesRead, want)
	}
}

type manifestTestFile struct {
	Path string
	Kind FileKind
	Rows int64
}

func writeJSONLFile(t *testing.T, root, rel string, lines ...string) {
	t.Helper()
	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	writeBundleFile(t, root, rel, []byte(body))
}

func writeManifestForFiles(t *testing.T, root string, hashSlotCount uint16, files []manifestTestFile) {
	t.Helper()
	defaulted := make([]manifestTestFile, len(files))
	copy(defaulted, files)
	for i := range defaulted {
		defaulted[i].Rows = -1
	}
	writeManifestForFilesWithRows(t, root, hashSlotCount, defaulted)
}

func writeManifestForFilesWithRows(t *testing.T, root string, hashSlotCount uint16, files []manifestTestFile) {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"format":"wkdb-import-bundle","version":1,"hash_slot_count":`)
	b.WriteString(itoa(int(hashSlotCount)))
	b.WriteString(`,"files":[`)
	for i, file := range files {
		if i > 0 {
			b.WriteByte(',')
		}
		data, err := os.ReadFile(filepath.Join(root, file.Path))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", file.Path, err)
		}
		sum := sha256.Sum256(data)
		b.WriteString(`{"path":"`)
		b.WriteString(file.Path)
		b.WriteString(`","kind":"`)
		b.WriteString(string(file.Kind))
		b.WriteString(`","rows":`)
		rows := file.Rows
		if rows < 0 {
			rows = int64(strings.Count(string(data), "\n"))
		}
		b.WriteString(itoa(int(rows)))
		b.WriteString(`,"sha256":"`)
		b.WriteString(hex.EncodeToString(sum[:]))
		b.WriteString(`"}`)
	}
	b.WriteString(`]}`)
	writeManifest(t, root, b.String())
}

func bundleFilesSize(t *testing.T, root string, files []manifestTestFile) int64 {
	t.Helper()
	var size int64
	for _, file := range files {
		info, err := os.Stat(filepath.Join(root, file.Path))
		if err != nil {
			t.Fatalf("Stat(%s): %v", file.Path, err)
		}
		size += info.Size()
	}
	return size
}

func itoa(n int) string { return strconv.Itoa(n) }
