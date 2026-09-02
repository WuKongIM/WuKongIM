package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	msgdb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestDecodeRecordRejectsSchemaCorruptionAcrossDatasets(t *testing.T) {
	tests := []struct {
		name string
		kind FileKind
		line string
		want string
	}{
		{name: "user unknown field", kind: FileKindMetaUsers, line: `{"uid":"u1","future":1}`, want: "unknown field"},
		{name: "device missing uid", kind: FileKindMetaDevices, line: `{"device_flag":1}`, want: "uid"},
		{name: "channel missing id", kind: FileKindMetaChannels, line: `{"channel_type":2}`, want: "channel_id"},
		{name: "subscriber missing uid", kind: FileKindMetaSubscribers, line: `{"channel_id":"g1","channel_type":2}`, want: "uid"},
		{name: "ordinary membership missing channel", kind: FileKindMetaUserChannelMemberships, line: `{"uid":"u1"}`, want: "channel_id"},
		{name: "command membership missing channel", kind: FileKindMetaUserCMDChannelMemberships, line: `{"uid":"u1"}`, want: "command_channel_id"},
		{name: "latest payload corrupt", kind: FileKindMetaChannelLatest, line: `{"channel_id":"g1","last_payload_b64":"@@"}`, want: "last_payload_b64"},
		{name: "directory task missing channel", kind: FileKindMetaPersonDirectoryTasks, line: `{"generation":1}`, want: "channel_id"},
		{name: "message channel missing id", kind: FileKindMessageChannels, line: `{"channel_key":"g1:2","channel_type":2}`, want: "channel_id"},
		{name: "message sequence zero", kind: FileKindMessageMessages, line: `{"channel_key":"g1:2","message_seq":0,"message_id":1,"payload_b64":""}`, want: "message_seq"},
		{name: "message id zero", kind: FileKindMessageMessages, line: `{"channel_key":"g1:2","message_seq":1,"message_id":0,"payload_b64":""}`, want: "message_id"},
		{name: "unknown dataset", kind: FileKind("future.rows"), line: `{}`, want: "unknown kind"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeRecord(test.kind, []byte(test.line))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("decodeRecord() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReadJSONLPropagatesLineVisitorContextAndReaderFailures(t *testing.T) {
	t.Run("visitor includes physical line", func(t *testing.T) {
		visitErr := errors.New("stop visit")
		input := "\n" + `{"hash_slot":1,"uid":"u1"}` + "\n"
		err := readJSONL(context.Background(), strings.NewReader(input), FileKindMetaUsers, func(any) error {
			return visitErr
		})
		if !errors.Is(err, visitErr) || !strings.Contains(err.Error(), "line 2: visit") {
			t.Fatalf("readJSONL() error = %v", err)
		}
	})

	t.Run("canceled before visit", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		visited := false
		err := readJSONL(ctx, strings.NewReader(`{"hash_slot":1,"uid":"u1"}`+"\n"), FileKindMetaUsers, func(any) error {
			visited = true
			return nil
		})
		if !errors.Is(err, context.Canceled) || visited {
			t.Fatalf("readJSONL() error/visited = %v/%v, want context.Canceled/false", err, visited)
		}
	})

	t.Run("reader failure after valid row", func(t *testing.T) {
		readErr := errors.New("source read failed")
		reader := io.MultiReader(
			strings.NewReader(`{"hash_slot":1,"uid":"u1"}`+"\n"),
			staticErrorReader{err: readErr},
		)
		visits := 0
		err := readJSONL(context.Background(), reader, FileKindMetaUsers, func(any) error {
			visits++
			return nil
		})
		if !errors.Is(err, readErr) || visits != 1 || !strings.Contains(err.Error(), "scan jsonl") {
			t.Fatalf("readJSONL() error/visits = %v/%d", err, visits)
		}
	})
}

func TestUint64JSONRejectsInexactOrOutOfRangeValues(t *testing.T) {
	invalid := []string{
		`-1`,
		`18446744073709551616`,
		`1.5`,
		`true`,
		`null`,
		`""`,
		`" 1"`,
	}
	for _, raw := range invalid {
		var value Uint64
		if err := value.UnmarshalJSON([]byte(raw)); err == nil {
			t.Fatalf("UnmarshalJSON(%s) error = nil", raw)
		}
	}

	var value Uint64
	if err := value.UnmarshalJSON([]byte(`"18446744073709551615"`)); err != nil || uint64(value) != ^uint64(0) {
		t.Fatalf("max uint64 decode = %d/%v", value, err)
	}
}

func TestLoadManifestRejectsMalformedEnvelopeBeforeImport(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "unknown field", content: `{"format":"wkdb-import-bundle","version":1,"hash_slot_count":16,"files":[],"future":true}`, want: "unknown field"},
		{name: "extra JSON", content: `{"format":"wkdb-import-bundle","version":1,"hash_slot_count":16,"files":[]} {}`, want: "extra JSON"},
		{name: "wrong format", content: `{"format":"other","version":1,"hash_slot_count":16,"files":[]}`, want: "format"},
		{name: "unsupported version", content: `{"format":"wkdb-import-bundle","version":2,"hash_slot_count":16,"files":[]}`, want: "version"},
		{name: "zero hash slots", content: `{"format":"wkdb-import-bundle","version":1,"hash_slot_count":0,"files":[]}`, want: "hash_slot_count"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeManifest(t, root, test.content)
			_, err := LoadManifest(root)
			if err == nil || !strings.Contains(err.Error(), test.want) || !errors.Is(err, ErrInvalidBundle) {
				t.Fatalf("LoadManifest() error = %v, want invalid bundle containing %q", err, test.want)
			}
		})
	}
}

func TestSafeBundlePathRejectsAmbiguousAndEscapingNames(t *testing.T) {
	invalid := []string{"", ".", "..", "../outside", "/absolute", "meta/../outside", "meta\\users.jsonl", "meta//users.jsonl"}
	for _, raw := range invalid {
		if _, err := safeBundlePath(raw); err == nil {
			t.Fatalf("safeBundlePath(%q) error = nil", raw)
		}
	}
	if got, err := safeBundlePath("meta/users.jsonl"); err != nil || got != "meta/users.jsonl" {
		t.Fatalf("safeBundlePath(valid) = %q/%v", got, err)
	}
}

func TestOpenBundleFileRejectsUnsafeMissingAndNonRegularEntries(t *testing.T) {
	root := t.TempDir()
	if _, _, err := openBundleFile(root, FileEntry{Path: "../outside"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("unsafe path error = %v, want ErrValidation", err)
	}
	if _, _, err := openBundleFile(root, FileEntry{Path: "missing.jsonl"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing file error = %v, want ErrValidation", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "directory.jsonl"), 0o755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	if _, _, err := openBundleFile(root, FileEntry{Path: "directory.jsonl"}); !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "non-regular") {
		t.Fatalf("directory error = %v, want non-regular ErrValidation", err)
	}

	writeBundleFile(t, root, "valid.jsonl", []byte("{}\n"))
	file, size, err := openBundleFile(root, FileEntry{Path: "valid.jsonl"})
	if err != nil {
		t.Fatalf("openBundleFile(valid): %v", err)
	}
	if size != 3 {
		t.Fatalf("valid file size = %d, want 3", size)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(valid): %v", err)
	}
}

func TestPrepareExportRootEnforcesSafeOverwriteBoundary(t *testing.T) {
	for _, unsafe := range []string{"", ".", string(os.PathSeparator)} {
		if err := prepareExportRoot(unsafe, true); err == nil {
			t.Fatalf("prepareExportRoot(%q, overwrite) error = nil", unsafe)
		}
	}

	base := t.TempDir()
	missing := filepath.Join(base, "nested", "bundle")
	if err := prepareExportRoot(missing, false); err != nil {
		t.Fatalf("prepareExportRoot(missing): %v", err)
	}
	if info, err := os.Stat(missing); err != nil || !info.IsDir() {
		t.Fatalf("created export root stat = %v/%v", info, err)
	}

	regular := filepath.Join(base, "regular-file")
	if err := os.WriteFile(regular, []byte("data"), 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	if err := prepareExportRoot(regular, false); err == nil {
		t.Fatal("regular file accepted as export directory")
	}

	nonEmpty := filepath.Join(base, "non-empty")
	if err := os.MkdirAll(nonEmpty, 0o755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	stale := filepath.Join(nonEmpty, "stale")
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatalf("WriteFile(stale): %v", err)
	}
	if err := prepareExportRoot(nonEmpty, false); err == nil {
		t.Fatal("non-empty export directory accepted without overwrite")
	}
	if err := prepareExportRoot(nonEmpty, true); err != nil {
		t.Fatalf("prepareExportRoot(overwrite): %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale file survived overwrite: %v", err)
	}

	link := filepath.Join(base, "bundle-link")
	if err := os.Symlink(nonEmpty, link); err == nil {
		if err := prepareExportRoot(link, false); err == nil {
			t.Fatal("symlink accepted as export directory")
		}
	}
}

func TestExportMessageFileSetSplitsAndChecksumsAtRowBoundary(t *testing.T) {
	root := t.TempDir()
	stats := ExportStats{}
	files := newExportMessageFileSet(root, 2, &stats)
	for seq := uint64(1); seq <= 3; seq++ {
		if err := files.Write(MessageRecord{
			ChannelKey: "g1:2", MessageSeq: Uint64(seq), MessageID: Uint64(1000 + seq), PayloadB64: "",
		}); err != nil {
			t.Fatalf("Write(seq=%d): %v", seq, err)
		}
	}
	entries, err := files.Close()
	if err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if len(entries) != 2 || entries[0].Rows != 2 || entries[1].Rows != 1 {
		t.Fatalf("message entries = %+v, want row split 2/1", entries)
	}
	if stats.FilesWritten != 2 || stats.RowsExported != 3 || stats.MessagesExported != 3 {
		t.Fatalf("export stats = %+v", stats)
	}
	for _, entry := range entries {
		actual, err := fileSHA256(filepath.Join(root, filepath.FromSlash(entry.Path)))
		if err != nil {
			t.Fatalf("fileSHA256(%s): %v", entry.Path, err)
		}
		if actual != entry.SHA256 {
			t.Fatalf("checksum for %s = %s, want %s", entry.Path, entry.SHA256, actual)
		}
	}
}

func TestExportFileWriterIsIdempotentlyClosedAndRejectsLateWrites(t *testing.T) {
	root := t.TempDir()
	writer, err := newExportFileWriter(root, "meta/users.jsonl", FileKindMetaUsers)
	if err != nil {
		t.Fatalf("newExportFileWriter(): %v", err)
	}
	if err := writer.Write(UserRecord{HashSlot: 1, UID: "u1"}); err != nil {
		t.Fatalf("Write(): %v", err)
	}
	first, err := writer.Close()
	if err != nil {
		t.Fatalf("Close(first): %v", err)
	}
	second, err := writer.Close()
	if err != nil || second != first {
		t.Fatalf("Close(second) = %+v/%v, want %+v", second, err, first)
	}
	if err := writer.Write(UserRecord{HashSlot: 1, UID: "late"}); err == nil {
		t.Fatal("Write() after Close succeeded")
	}
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(first.Path)))
	if err != nil {
		t.Fatalf("ReadFile(): %v", err)
	}
	sum := sha256.Sum256(body)
	if first.Rows != 1 || first.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("file entry = %+v", first)
	}
}

func TestMessageChannelStreamValidatesCrossFileCursorBoundaries(t *testing.T) {
	t.Run("ordered lookup across files", func(t *testing.T) {
		root := t.TempDir()
		writeJSONLFile(t, root, "message/channels-1.jsonl", `{"channel_key":"a:2","channel_id":"a","channel_type":2}`)
		writeJSONLFile(t, root, "message/channels-2.jsonl", `{"channel_key":"b:2","channel_id":"b","channel_type":2}`)
		entries := []FileEntry{
			{Path: "message/channels-1.jsonl", Kind: FileKindMessageChannels, Rows: 1},
			{Path: "message/channels-2.jsonl", Kind: FileKindMessageChannels, Rows: 1},
		}
		stats := ImportStats{}
		stream := newMessageChannelStream(context.Background(), root, entries, &stats)
		row, ok, err := stream.Lookup("b:2")
		if err != nil || !ok || row.ChannelID != "b" {
			t.Fatalf("Lookup(b:2) = %+v/%v/%v", row, ok, err)
		}
		if err := stream.Drain(); err != nil {
			t.Fatalf("Drain(): %v", err)
		}
		if stats.Files != 2 || stats.RowsValidated != 2 {
			t.Fatalf("stream stats = %+v", stats)
		}
	})

	t.Run("duplicate across files", func(t *testing.T) {
		root := t.TempDir()
		line := `{"channel_key":"a:2","channel_id":"a","channel_type":2}`
		writeJSONLFile(t, root, "message/channels-1.jsonl", line)
		writeJSONLFile(t, root, "message/channels-2.jsonl", line)
		stream := newMessageChannelStream(context.Background(), root, []FileEntry{
			{Path: "message/channels-1.jsonl", Kind: FileKindMessageChannels, Rows: 1},
			{Path: "message/channels-2.jsonl", Kind: FileKindMessageChannels, Rows: 1},
		}, nil)
		defer stream.Close()
		if err := stream.Drain(); err == nil || !strings.Contains(err.Error(), "duplicate message channel") {
			t.Fatalf("Drain() error = %v, want duplicate", err)
		}
	})

	t.Run("row count mismatch", func(t *testing.T) {
		root := t.TempDir()
		writeJSONLFile(t, root, "message/channels.jsonl", `{"channel_key":"a:2","channel_id":"a","channel_type":2}`)
		stream := newMessageChannelStream(context.Background(), root, []FileEntry{
			{Path: "message/channels.jsonl", Kind: FileKindMessageChannels, Rows: 2},
		}, nil)
		defer stream.Close()
		if err := stream.Drain(); err == nil || !strings.Contains(err.Error(), "row count mismatch") {
			t.Fatalf("Drain() error = %v, want row count mismatch", err)
		}
	})

	t.Run("corrupt catalog row", func(t *testing.T) {
		root := t.TempDir()
		writeJSONLFile(t, root, "message/channels.jsonl", `{`)
		stream := newMessageChannelStream(context.Background(), root, []FileEntry{
			{Path: "message/channels.jsonl", Kind: FileKindMessageChannels, Rows: 1},
		}, nil)
		defer stream.Close()
		if err := stream.Drain(); err == nil || !strings.Contains(err.Error(), "line 1") {
			t.Fatalf("Drain() error = %v, want corrupt line", err)
		}
	})
}

func TestInspectNumericConversionRejectsCorruptRanges(t *testing.T) {
	row := map[string]any{"value": uint64(^uint64(0))}
	if _, err := rowInt64(row, "value"); !errors.Is(err, ErrValidation) {
		t.Fatalf("rowInt64 overflow error = %v, want ErrValidation", err)
	}
	if _, err := rowUint64(map[string]any{"value": int64(-1)}, "value"); err == nil {
		t.Fatal("rowUint64 accepted negative value")
	}
	if _, err := rowUint8(map[string]any{"value": uint64(256)}, "value"); err == nil {
		t.Fatal("rowUint8 accepted value above 255")
	}
}

func TestExportRecordAdaptersRejectCorruptInspectRows(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "user device level type",
			run: func() error {
				_, err := exportUserRecord(1, metadb.InspectRow{
					"uid": "u1", "token": "token", "device_flag": int64(1), "device_level": "bad",
				})
				return err
			},
		},
		{
			name: "device level type",
			run: func() error {
				_, err := exportDeviceRecord(1, metadb.InspectRow{
					"uid": "u1", "device_flag": int64(1), "token": "token", "device_level": "bad",
				})
				return err
			},
		},
		{
			name: "channel directory state range",
			run: func() error {
				_, err := exportChannelRecord(1, metadb.InspectRow{
					"channel_id": "g1", "channel_type": int64(2), "ban": int64(0), "disband": int64(0),
					"send_ban": int64(0), "allow_stranger": int64(1), "large": int64(0),
					"subscriber_mutation_version": uint64(1), "directory_projection_state": uint64(255),
					"directory_projection_generation": uint64(1),
				})
				return err
			},
		},
		{
			name: "person directory generation negative",
			run: func() error {
				_, err := exportPersonDirectoryTaskRecord(1, metadb.InspectRow{
					"channel_id": "g1", "channel_type": int64(1), "committed_tail": uint64(1),
					"created_at": int64(2), "generation": int64(-1),
				})
				return err
			},
		},
		{
			name: "subscriber uid type",
			run: func() error {
				_, err := exportSubscriberRecord(1, metadb.InspectRow{
					"channel_id": "g1", "channel_type": int64(2), "uid": uint64(1),
				})
				return err
			},
		},
		{
			name: "ordinary membership update type",
			run: func() error {
				_, err := exportUserChannelMembershipRecord(1, metadb.InspectRow{
					"uid": "u1", "channel_id": "g1", "channel_type": int64(2),
					"join_seq": uint64(1), "read_seq": uint64(1), "deleted_to_seq": uint64(0),
					"activated_at": int64(1), "tombstone": false, "tombstone_at": int64(0),
					"source_version": uint64(1), "updated_at": "bad",
				})
				return err
			},
		},
		{
			name: "command membership update type",
			run: func() error {
				_, err := exportUserCMDChannelMembershipRecord(1, metadb.InspectRow{
					"uid": "u1", "command_channel_id": "cmd", "channel_type": int64(2),
					"start_seq": uint64(1), "ack_seq": uint64(1), "tombstone": false,
					"tombstone_at": int64(0), "updated_at": "bad",
				})
				return err
			},
		},
		{
			name: "channel latest update type",
			run: func() error {
				_, err := exportChannelLatestRecord(1, metadb.InspectRow{
					"channel_id": "g1", "channel_type": int64(2), "last_message_id": uint64(1),
					"last_message_seq": uint64(1), "last_at": int64(1), "from_uid": "u1",
					"client_msg_no": "c1", "payload": []byte("x"), "updated_at": "bad",
				})
				return err
			},
		},
		{
			name: "message channel type overflow",
			run: func() error {
				_, err := exportMessageChannelRecord(msgdb.InspectMessageRow{
					"channel_key": "g1:2", "channel_id": "g1", "channel_type": uint64(256),
				})
				return err
			},
		},
		{
			name: "message payload type",
			run: func() error {
				_, err := exportMessageRecord("g1:2", msgdb.InspectMessageRow{
					"message_seq": uint64(1), "message_id": uint64(1), "client_msg_no": "c1",
					"from_uid": "u1", "server_timestamp_ms": int64(1), "payload": "bad",
				})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, ErrValidation) {
				t.Fatalf("export adapter error = %v, want ErrValidation", err)
			}
		})
	}
}

func TestBundleValidatorRejectsInvalidProjectionAndTaskState(t *testing.T) {
	validator := newBundleValidator(16)
	tests := []struct {
		name   string
		kind   FileKind
		record any
		want   string
	}{
		{
			name: "unknown directory state", kind: FileKindMetaChannels,
			record: ChannelRecord{ChannelID: "g1", DirectoryProjectionState: uint8(metadb.DirectoryProjectionReady) + 1},
			want:   "directory_projection_state",
		},
		{
			name: "none with generation", kind: FileKindMetaChannels,
			record: ChannelRecord{ChannelID: "g1", DirectoryProjectionState: uint8(metadb.DirectoryProjectionNone), DirectoryProjectionGeneration: 1},
			want:   "generation",
		},
		{
			name: "directory task without generation", kind: FileKindMetaPersonDirectoryTasks,
			record: PersonDirectoryTaskRecord{ChannelID: "g1"},
			want:   "generation is zero",
		},
		{name: "unknown kind", kind: FileKind("future.rows"), record: struct{}{}, want: "unknown kind"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validator.Visit(test.kind, test.record)
			if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Visit() error = %v, want ErrValidation containing %q", err, test.want)
			}
		})
	}
}

func TestSubscriberCompositeOrderContract(t *testing.T) {
	tests := []struct {
		name        string
		left, right subscriberOrder
		want        int
	}{
		{name: "equal", left: subscriberOrder{1, "a", 2, "u"}, right: subscriberOrder{1, "a", 2, "u"}, want: 0},
		{name: "hash slot ascending", left: subscriberOrder{1, "z", 9, "z"}, right: subscriberOrder{2, "a", 1, "a"}, want: -1},
		{name: "hash slot descending", left: subscriberOrder{2, "a", 1, "a"}, right: subscriberOrder{1, "z", 9, "z"}, want: 1},
		{name: "channel ascending", left: subscriberOrder{1, "a", 9, "z"}, right: subscriberOrder{1, "b", 1, "a"}, want: -1},
		{name: "channel descending", left: subscriberOrder{1, "b", 1, "a"}, right: subscriberOrder{1, "a", 9, "z"}, want: 1},
		{name: "type ascending", left: subscriberOrder{1, "a", 1, "z"}, right: subscriberOrder{1, "a", 2, "a"}, want: -1},
		{name: "type descending", left: subscriberOrder{1, "a", 2, "a"}, right: subscriberOrder{1, "a", 1, "z"}, want: 1},
		{name: "uid ascending", left: subscriberOrder{1, "a", 2, "a"}, right: subscriberOrder{1, "a", 2, "b"}, want: -1},
		{name: "uid descending", left: subscriberOrder{1, "a", 2, "b"}, right: subscriberOrder{1, "a", 2, "a"}, want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := compareSubscriberOrder(test.left, test.right); got != test.want {
				t.Fatalf("compareSubscriberOrder() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestValidateBundleRejectsMessageChannelKeyRegressionAcrossFiles(t *testing.T) {
	root := t.TempDir()
	writeJSONLFile(t, root, "message/channels.jsonl",
		`{"channel_key":"a:2","channel_id":"a","channel_type":2}`,
		`{"channel_key":"b:2","channel_id":"b","channel_type":2}`,
	)
	writeJSONLFile(t, root, "message/messages-1.jsonl", `{"channel_key":"b:2","message_seq":1,"message_id":2,"payload_b64":""}`)
	writeJSONLFile(t, root, "message/messages-2.jsonl", `{"channel_key":"a:2","message_seq":1,"message_id":1,"payload_b64":""}`)
	writeManifestForFiles(t, root, 16, []manifestTestFile{
		{Path: "message/channels.jsonl", Kind: FileKindMessageChannels},
		{Path: "message/messages-1.jsonl", Kind: FileKindMessageMessages},
		{Path: "message/messages-2.jsonl", Kind: FileKindMessageMessages},
	})
	if _, err := ValidateBundle(context.Background(), root, ImportOptions{HashSlotCount: 16}); !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "message order violation") {
		t.Fatalf("ValidateBundle() error = %v, want message order violation", err)
	}
}

func TestTransferTopLevelAPIsRejectMissingPreconditions(t *testing.T) {
	ctx := context.Background()
	if _, err := ExportBundle(ctx, filepath.Join(t.TempDir(), "nil"), nil, ExportOptions{HashSlotCount: 16}); err == nil {
		t.Fatal("ExportBundle(nil store) error = nil")
	}
	zeroSlotRoot := filepath.Join(t.TempDir(), "zero-slots")
	if _, err := ExportBundle(ctx, zeroSlotRoot, new(inspect.Store), ExportOptions{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("ExportBundle(zero slots) error = %v, want ErrValidation", err)
	}
	if _, err := os.Stat(zeroSlotRoot); !os.IsNotExist(err) {
		t.Fatalf("zero-slot export mutated output root: %v", err)
	}
	if _, err := ImportBundle(ctx, t.TempDir(), nil, ImportOptions{}); err == nil {
		t.Fatal("ImportBundle(nil store) error = nil")
	}
	if _, err := VerifyStores(ctx, nil, nil, VerifyOptions{HashSlotCount: 16}); !errors.Is(err, ErrValidation) {
		t.Fatalf("VerifyStores(nil stores) error = %v, want ErrValidation", err)
	}
}

func TestDigestMessageRowRejectsCorruptInspectFields(t *testing.T) {
	base := msgdb.InspectMessageRow{
		"message_seq": uint64(1), "message_id": uint64(1001),
		"client_msg_no": "c1", "from_uid": "u1", "server_timestamp_ms": int64(10),
		"payload_hash": uint64(20), "payload_size": uint64(3), "payload": []byte("abc"),
	}
	for _, field := range []string{"message_seq", "message_id", "client_msg_no", "from_uid", "server_timestamp_ms", "payload_hash", "payload_size", "payload"} {
		t.Run(field, func(t *testing.T) {
			row := make(msgdb.InspectMessageRow, len(base))
			for key, value := range base {
				row[key] = value
			}
			delete(row, field)
			if _, _, err := digestMessageRow("g1:2", row); !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), field) {
				t.Fatalf("digestMessageRow() error = %v, want missing %s", err, field)
			}
		})
	}
}

type staticErrorReader struct {
	err error
}

func (r staticErrorReader) Read([]byte) (int, error) {
	return 0, r.err
}
