package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db"
	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	msgdb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/WuKongIM/WuKongIM/pkg/hashslot"
)

func TestRunRejectsUnknownCommand(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithIO([]string{"missing"}, nil, &stderr)
	if code == 0 {
		t.Fatal("exit code = 0, want failure")
	}
	if stderr.Len() == 0 {
		t.Fatal("stderr is empty")
	}
}

func TestRunRejectsUnknownFormatBeforeOpeningStore(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithIO([]string{"--format", "yaml", "query", "show tables"}, nil, &stderr)
	if code != exitConfig {
		t.Fatalf("exit code = %d, want %d", code, exitConfig)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("unknown format")) {
		t.Fatalf("stderr = %q, want format error", stderr.String())
	}
}

func TestRunImportDryRunAcceptsCommandLocalFlags(t *testing.T) {
	bundleRoot := t.TempDir()
	writeWKDBBundle(t, bundleRoot, 16, nil)

	var stdout, stderr bytes.Buffer
	code := runWithStreams([]string{"import", "--dry-run", "--input", bundleRoot}, nil, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"validated=0", "written=0", "messages=0", "subscribers=0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, want %s", out, want)
		}
	}
}

func TestRunImportRejectsMissingInput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWithStreams([]string{"import", "--dry-run"}, nil, &stdout, &stderr)
	if code != exitConfig {
		t.Fatalf("exit code = %d, want %d", code, exitConfig)
	}
	if !strings.Contains(stderr.String(), "--input") {
		t.Fatalf("stderr = %q, want --input error", stderr.String())
	}
}

func TestRunImportRequiresExplicitEmptyTarget(t *testing.T) {
	bundleRoot := writeMinimalImportBundle(t)
	dataDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := runWithStreams([]string{"--data-dir", dataDir, "import", "--input", bundleRoot}, nil, &stdout, &stderr)
	if code != exitConfig {
		t.Fatalf("exit code = %d, want %d", code, exitConfig)
	}
	if !strings.Contains(stderr.String(), "--require-empty") {
		t.Fatalf("stderr = %q, want --require-empty error", stderr.String())
	}
	for _, rel := range []string{"data", "channellog"} {
		if _, err := os.Stat(filepath.Join(dataDir, rel)); !os.IsNotExist(err) {
			t.Fatalf("%s stat err = %v, want not exist", rel, err)
		}
	}
}

func TestRunImportWritesTempNodeStore(t *testing.T) {
	bundleRoot := writeMinimalImportBundle(t)
	dataDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := runWithStreams([]string{
		"--data-dir", dataDir,
		"--hash-slot-count", "16",
		"import",
		"--input", bundleRoot,
		"--require-empty",
		"--subscriber-batch-size", "1",
		"--message-batch-size", "1",
		"--message-batch-bytes", "1024",
	}, nil, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"validated=5", "written=4", "messages=1", "subscribers=1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, want %s", out, want)
		}
	}

	store, err := db.OpenNodeStore(db.NodeStoreOptions{
		MetaPath:    filepath.Join(dataDir, "data"),
		MessagePath: filepath.Join(dataDir, "channellog"),
	})
	if err != nil {
		t.Fatalf("OpenNodeStore(): %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	userSlot := hashslot.HashSlotForKey("u1", 16)
	user, ok, err := store.Meta().HashSlot(userSlot).GetUser(ctx, "u1")
	if err != nil || !ok {
		t.Fatalf("GetUser() ok=%v err=%v, want ok", ok, err)
	}
	if user.Token != "user-token" {
		t.Fatalf("user.Token = %q, want user-token", user.Token)
	}
	messages, err := store.Messages().Channel("g1:2", msgdb.ChannelID{ID: "g1", Type: 2}).Read(ctx, 1, msgdb.ReadOptions{Limit: 1})
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	if len(messages) != 1 || messages[0].MessageID != 1001 || string(messages[0].Payload) != "hi" {
		t.Fatalf("messages = %+v, want imported message", messages)
	}
}

func TestExportCommandWritesImportBundle(t *testing.T) {
	bundleRoot := writeMinimalImportBundle(t)
	dataDir := t.TempDir()

	var importStdout, importStderr bytes.Buffer
	importCode := runWithStreams([]string{
		"--data-dir", dataDir,
		"--hash-slot-count", "16",
		"import",
		"--input", bundleRoot,
		"--require-empty",
	}, nil, &importStdout, &importStderr)
	if importCode != exitOK {
		t.Fatalf("import exit code = %d, stderr = %q", importCode, importStderr.String())
	}

	exportRoot := filepath.Join(t.TempDir(), "exported")
	var stdout, stderr bytes.Buffer
	code := runWithStreams([]string{
		"--data-dir", dataDir,
		"--hash-slot-count", "16",
		"export",
		"--output", exportRoot,
		"--page-size", "1",
		"--message-file-rows", "1",
	}, nil, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"exported=", "messages=1", "subscribers=1", "channels=1", "files="} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, want %s", out, want)
		}
	}
	if _, err := transfer.ValidateBundle(context.Background(), exportRoot, transfer.ImportOptions{HashSlotCount: 16}); err != nil {
		t.Fatalf("ValidateBundle(exported): %v", err)
	}
}

func TestDiffCommandReturnsOKForEqualStores(t *testing.T) {
	bundleRoot := writeMinimalImportBundle(t)
	sourceDir := t.TempDir()
	importBundleToDataDir(t, bundleRoot, sourceDir)

	exportRoot := filepath.Join(t.TempDir(), "exported")
	var exportStdout, exportStderr bytes.Buffer
	exportCode := runWithStreams([]string{
		"--data-dir", sourceDir,
		"--hash-slot-count", "16",
		"export",
		"--output", exportRoot,
	}, nil, &exportStdout, &exportStderr)
	if exportCode != exitOK {
		t.Fatalf("export exit code = %d, stderr = %q", exportCode, exportStderr.String())
	}

	targetDir := t.TempDir()
	importBundleToDataDir(t, exportRoot, targetDir)

	var stdout, stderr bytes.Buffer
	code := runWithStreams([]string{
		"--hash-slot-count", "16",
		"diff",
		"--source-data-dir", sourceDir,
		"--target-data-dir", targetDir,
		"--mode", "full",
	}, nil, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "equal=true") {
		t.Fatalf("stdout = %q, want equal=true", stdout.String())
	}
}

func TestDiffCommandReturnsQueryExitForMismatch(t *testing.T) {
	sourceDir := t.TempDir()
	importBundleToDataDir(t, writeMinimalImportBundleWithUserToken(t, "user-token"), sourceDir)
	targetDir := t.TempDir()
	importBundleToDataDir(t, writeMinimalImportBundleWithUserToken(t, "changed-token"), targetDir)

	var stdout, stderr bytes.Buffer
	code := runWithStreams([]string{
		"--hash-slot-count", "16",
		"diff",
		"--source-data-dir", sourceDir,
		"--target-data-dir", targetDir,
	}, nil, &stdout, &stderr)
	if code != exitQuery {
		t.Fatalf("exit code = %d, want %d; stderr = %q", code, exitQuery, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "equal=false") {
		t.Fatalf("stdout = %q, want equal=false", stdout.String())
	}
}

func TestRunQueryShowTables(t *testing.T) {
	metaPath := t.TempDir()
	metaDB, err := meta.Open(metaPath)
	if err != nil {
		t.Fatalf("meta.Open(): %v", err)
	}
	if err := metaDB.Close(); err != nil {
		t.Fatalf("meta Close(): %v", err)
	}
	store, err := inspect.OpenStore(inspect.Options{MetaPath: metaPath})
	if err != nil {
		t.Fatalf("OpenStore(): %v", err)
	}
	defer store.Close()

	var stdout, stderr bytes.Buffer
	code := runQuery(context.Background(), store, "table", "show tables", &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("meta.user")) {
		t.Fatalf("stdout = %q, want meta.user", stdout.String())
	}
}

func importBundleToDataDir(t *testing.T, bundleRoot, dataDir string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runWithStreams([]string{
		"--data-dir", dataDir,
		"--hash-slot-count", "16",
		"import",
		"--input", bundleRoot,
		"--require-empty",
	}, nil, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("import exit code = %d, stderr = %q", code, stderr.String())
	}
}

func TestRunREPLReturnsFailureAfterQueryError(t *testing.T) {
	metaPath := t.TempDir()
	metaDB, err := meta.Open(metaPath)
	if err != nil {
		t.Fatalf("meta.Open(): %v", err)
	}
	if err := metaDB.Close(); err != nil {
		t.Fatalf("meta Close(): %v", err)
	}
	store, err := inspect.OpenStore(inspect.Options{MetaPath: metaPath})
	if err != nil {
		t.Fatalf("OpenStore(): %v", err)
	}
	defer store.Close()

	var stdout, stderr bytes.Buffer
	code := runREPL(context.Background(), store, "table", bytes.NewBufferString("select * from meta.user limit 1\n"), &stdout, &stderr)
	if code != exitQuery {
		t.Fatalf("exit code = %d, want %d", code, exitQuery)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("query error")) {
		t.Fatalf("stderr = %q, want query error", stderr.String())
	}
}

type wkdbBundleFile struct {
	path  string
	kind  transfer.FileKind
	lines []string
}

func writeMinimalImportBundle(t *testing.T) string {
	t.Helper()
	return writeMinimalImportBundleWithUserToken(t, "user-token")
}

func writeMinimalImportBundleWithUserToken(t *testing.T, userToken string) string {
	t.Helper()
	root := t.TempDir()
	userSlot := hashslot.HashSlotForKey("u1", 16)
	channelSlot := hashslot.HashSlotForKey("g1", 16)
	writeWKDBBundle(t, root, 16, []wkdbBundleFile{
		{
			path: "meta/users.jsonl",
			kind: transfer.FileKindMetaUsers,
			lines: []string{
				`{"hash_slot":` + strconv.Itoa(int(userSlot)) + `,"uid":"u1","token":"` + userToken + `","device_flag":1,"device_level":2}`,
			},
		},
		{
			path: "meta/channels.jsonl",
			kind: transfer.FileKindMetaChannels,
			lines: []string{
				`{"hash_slot":` + strconv.Itoa(int(channelSlot)) + `,"channel_id":"g1","channel_type":2,"ban":0,"disband":0,"send_ban":0,"allow_stranger":1,"large":0,"subscriber_mutation_version":7}`,
			},
		},
		{
			path: "meta/subscribers.jsonl",
			kind: transfer.FileKindMetaSubscribers,
			lines: []string{
				`{"hash_slot":` + strconv.Itoa(int(channelSlot)) + `,"channel_id":"g1","channel_type":2,"uid":"u1"}`,
			},
		},
		{
			path: "message/channels.jsonl",
			kind: transfer.FileKindMessageChannels,
			lines: []string{
				`{"channel_key":"g1:2","channel_id":"g1","channel_type":2}`,
			},
		},
		{
			path: "message/messages-000001.jsonl",
			kind: transfer.FileKindMessageMessages,
			lines: []string{
				`{"channel_key":"g1:2","message_seq":1,"message_id":1001,"client_msg_no":"c1","from_uid":"u1","server_timestamp_ms":3000,"payload_b64":"aGk="}`,
			},
		},
	})
	return root
}

func writeWKDBBundle(t *testing.T, root string, hashSlotCount uint16, files []wkdbBundleFile) {
	t.Helper()
	for _, file := range files {
		writeWKDBBundleFile(t, root, file.path, file.lines)
	}
	var manifest strings.Builder
	manifest.WriteString(`{"format":"wkdb-import-bundle","version":1,"hash_slot_count":`)
	manifest.WriteString(strconv.Itoa(int(hashSlotCount)))
	manifest.WriteString(`,"files":[`)
	for i, file := range files {
		if i > 0 {
			manifest.WriteByte(',')
		}
		data, err := os.ReadFile(filepath.Join(root, file.path))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", file.path, err)
		}
		sum := sha256.Sum256(data)
		manifest.WriteString(`{"path":"`)
		manifest.WriteString(file.path)
		manifest.WriteString(`","kind":"`)
		manifest.WriteString(string(file.kind))
		manifest.WriteString(`","rows":`)
		manifest.WriteString(strconv.Itoa(strings.Count(string(data), "\n")))
		manifest.WriteString(`,"sha256":"`)
		manifest.WriteString(hex.EncodeToString(sum[:]))
		manifest.WriteString(`"}`)
	}
	manifest.WriteString(`]}`)
	writeWKDBBundleFile(t, root, "manifest.json", []string{manifest.String()})
}

func writeWKDBBundleFile(t *testing.T, root, rel string, lines []string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
