# Wkdb Import Bundle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `wkdb import` so operators can import a versioned JSONL transfer bundle into the current node-local `pkg/db` metadata and message stores.

**Architecture:** Add a new `pkg/db/transfer` package that owns the bundle manifest, JSONL parsers, semantic validation, dry-run validation, and typed writes into `pkg/db`. Keep `cmd/wkdb` as a CLI adapter that resolves config, parses import flags, calls transfer APIs, and reports progress. Import writes use current `pkg/db/meta` and `pkg/db/message` APIs rather than raw Pebble keys.

**Tech Stack:** Go standard library JSON/IO/hash packages, `pkg/db`, `pkg/db/meta`, `pkg/db/message`, `pkg/db/inspect`, `pkg/cluster.HashSlotForKey`, existing `cmd/wkdb` flag and config patterns.

---

## File Structure

- Create `pkg/db/transfer/types.go`: public bundle, option, stats, and record types with English comments.
- Create `pkg/db/transfer/manifest.go`: manifest loading, path safety, checksum verification, and file grouping.
- Create `pkg/db/transfer/manifest_test.go`: manifest and helper tests.
- Create `pkg/db/transfer/jsonl.go`: bounded JSONL scanner, exact numeric parsing, base64 parsing, and per-kind record decoding.
- Create `pkg/db/transfer/jsonl_test.go`: parser tests for required fields, uint64 strings, and payload bytes.
- Create `pkg/db/transfer/validate.go`: streaming semantic validation for hash slots, subscriber ordering, message catalog references, contiguous message sequences, and duplicate keys.
- Create `pkg/db/transfer/validate_test.go`: dry-run validation tests.
- Create `pkg/db/transfer/importer.go`: empty-target checks and typed metadata/message import.
- Create `pkg/db/transfer/importer_test.go`: real import tests against temporary stores.
- Create `cmd/wkdb/import.go`: command-specific import flag parsing and `runImport`.
- Modify `cmd/wkdb/main.go`: route the `import` subcommand and add import exit code.
- Modify `cmd/wkdb/config.go`: extend `cliFlags` with import-specific fields and comments.
- Modify `cmd/wkdb/main_test.go`: CLI import tests.
- Modify `cmd/wkdb/README.md`: describe explicit offline write import.
- Modify `docs/wiki/operations/wkdb-readonly-cli.md`: add import section while preserving read-only query semantics.

---

### Task 1: Manifest Contract And Safe File Loading

**Files:**
- Create: `pkg/db/transfer/types.go`
- Create: `pkg/db/transfer/manifest.go`
- Create: `pkg/db/transfer/manifest_test.go`

- [ ] **Step 1: Write failing manifest tests**

Create `pkg/db/transfer/manifest_test.go` with these tests:

```go
package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadManifestAcceptsValidBundle(t *testing.T) {
	root := t.TempDir()
	writeBundleFile(t, root, "meta/users.jsonl", []byte("{\"hash_slot\":1,\"uid\":\"u1\"}\n"))
	sum := sha256.Sum256([]byte("{\"hash_slot\":1,\"uid\":\"u1\"}\n"))
	writeManifest(t, root, `{
		"format":"wkdb-import-bundle",
		"version":1,
		"hash_slot_count":16,
		"files":[{"path":"meta/users.jsonl","kind":"meta.users","rows":1,"sha256":"`+hex.EncodeToString(sum[:])+`"}]
	}`)

	manifest, err := LoadManifest(root)
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	if manifest.HashSlotCount != 16 || len(manifest.Files) != 1 {
		t.Fatalf("manifest = %+v, want hash-slot count 16 and one file", manifest)
	}
}

func TestLoadManifestRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, `{
		"format":"wkdb-import-bundle",
		"version":1,
		"hash_slot_count":16,
		"files":[{"path":"../escape.jsonl","kind":"meta.users","rows":0,"sha256":"`+strings.Repeat("0", 64)+`"}]
	}`)

	_, err := LoadManifest(root)
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("LoadManifest() error = %v, want unsafe path", err)
	}
}

func TestLoadManifestRejectsChecksumMismatch(t *testing.T) {
	root := t.TempDir()
	writeBundleFile(t, root, "meta/users.jsonl", []byte("{}\n"))
	writeManifest(t, root, `{
		"format":"wkdb-import-bundle",
		"version":1,
		"hash_slot_count":16,
		"files":[{"path":"meta/users.jsonl","kind":"meta.users","rows":1,"sha256":"`+strings.Repeat("0", 64)+`"}]
	}`)

	_, err := LoadManifest(root)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("LoadManifest() error = %v, want checksum error", err)
	}
}

func TestLoadManifestRejectsUnknownKind(t *testing.T) {
	root := t.TempDir()
	writeBundleFile(t, root, "meta/unknown.jsonl", []byte(""))
	sum := sha256.Sum256(nil)
	writeManifest(t, root, `{
		"format":"wkdb-import-bundle",
		"version":1,
		"hash_slot_count":16,
		"files":[{"path":"meta/unknown.jsonl","kind":"meta.unknown","rows":0,"sha256":"`+hex.EncodeToString(sum[:])+`"}]
	}`)

	_, err := LoadManifest(root)
	if err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("LoadManifest() error = %v, want unknown kind", err)
	}
}

func writeBundleFile(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func writeManifest(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest.json): %v", err)
	}
}
```

- [ ] **Step 2: Run the manifest tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./pkg/db/transfer -run 'TestLoadManifest' -count=1
```

Expected: fail because `pkg/db/transfer` and `LoadManifest` do not exist.

- [ ] **Step 3: Add manifest types**

Create `pkg/db/transfer/types.go`:

```go
package transfer

import "errors"

const (
	bundleFormat  = "wkdb-import-bundle"
	bundleVersion = 1
)

var (
	// ErrInvalidBundle reports malformed import bundle input.
	ErrInvalidBundle = errors.New("transfer: invalid bundle")
	// ErrValidation reports semantically invalid import records.
	ErrValidation = errors.New("transfer: validation failed")
)

// FileKind identifies one bundle file record shape.
type FileKind string

const (
	// FileKindMetaUsers stores metadata user rows.
	FileKindMetaUsers FileKind = "meta.users"
	// FileKindMetaDevices stores metadata device rows.
	FileKindMetaDevices FileKind = "meta.devices"
	// FileKindMetaChannels stores metadata channel rows.
	FileKindMetaChannels FileKind = "meta.channels"
	// FileKindMetaSubscribers stores metadata subscriber rows.
	FileKindMetaSubscribers FileKind = "meta.subscribers"
	// FileKindMetaUserChannelMemberships stores UID-owned channel membership rows.
	FileKindMetaUserChannelMemberships FileKind = "meta.user_channel_memberships"
	// FileKindMetaConversations stores ordinary and CMD conversation rows.
	FileKindMetaConversations FileKind = "meta.conversations"
	// FileKindMetaChannelLatest stores channel latest projection rows.
	FileKindMetaChannelLatest FileKind = "meta.channel_latest"
	// FileKindMessageChannels stores message channel catalog rows.
	FileKindMessageChannels FileKind = "message.channels"
	// FileKindMessageMessages stores channel message rows.
	FileKindMessageMessages FileKind = "message.messages"
)

// Manifest describes one WKDB import bundle.
type Manifest struct {
	Format        string      `json:"format"`
	Version       int         `json:"version"`
	HashSlotCount uint16      `json:"hash_slot_count"`
	CreatedAtMS   int64       `json:"created_at_ms"`
	Files         []FileEntry `json:"files"`
}

// FileEntry describes one JSONL file inside a bundle.
type FileEntry struct {
	Path   string   `json:"path"`
	Kind   FileKind `json:"kind"`
	Rows   int64    `json:"rows"`
	SHA256 string   `json:"sha256"`
}

// ImportOptions configures bundle validation and import.
type ImportOptions struct {
	// HashSlotCount is the current cluster hash-slot count expected by the target store.
	HashSlotCount uint16
	// RequireEmpty rejects imports into non-empty target stores.
	RequireEmpty bool
	// DryRun validates the bundle without opening writable target stores.
	DryRun bool
	// SubscriberBatchSize bounds subscriber writes per channel mutation.
	SubscriberBatchSize int
	// MessageBatchSize bounds message writes per channel append batch.
	MessageBatchSize int
	// MessageBatchBytes bounds decoded payload bytes per channel append batch.
	MessageBatchBytes int
}

// ImportStats summarizes validated and imported rows.
type ImportStats struct {
	Files              int
	RowsValidated      int64
	RowsWritten        int64
	BytesRead          int64
	ChannelsImported   int64
	MessagesImported   int64
	SubscribersImported int64
}
```

- [ ] **Step 4: Add manifest loading implementation**

Create `pkg/db/transfer/manifest.go`:

```go
package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LoadManifest reads manifest.json and verifies every listed file.
func LoadManifest(root string) (Manifest, error) {
	manifestPath := filepath.Join(root, "manifest.json")
	file, err := os.Open(manifestPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: open manifest: %v", ErrInvalidBundle, err)
	}
	defer file.Close()

	var manifest Manifest
	dec := json.NewDecoder(file)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: decode manifest: %v", ErrInvalidBundle, err)
	}
	if err := validateManifest(root, manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateManifest(root string, manifest Manifest) error {
	if manifest.Format != bundleFormat {
		return fmt.Errorf("%w: unknown format %q", ErrInvalidBundle, manifest.Format)
	}
	if manifest.Version != bundleVersion {
		return fmt.Errorf("%w: unknown version %d", ErrInvalidBundle, manifest.Version)
	}
	if manifest.HashSlotCount == 0 {
		return fmt.Errorf("%w: hash_slot_count is required", ErrInvalidBundle)
	}
	for i, entry := range manifest.Files {
		if err := validateFileEntry(root, entry); err != nil {
			return fmt.Errorf("file[%d]: %w", i, err)
		}
	}
	return nil
}

func validateFileEntry(root string, entry FileEntry) error {
	if !knownFileKind(entry.Kind) {
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidBundle, entry.Kind)
	}
	path, err := safeBundlePath(root, entry.Path)
	if err != nil {
		return err
	}
	if entry.Rows < 0 {
		return fmt.Errorf("%w: negative rows for %s", ErrInvalidBundle, entry.Path)
	}
	want, err := hex.DecodeString(entry.SHA256)
	if err != nil || len(want) != sha256.Size {
		return fmt.Errorf("%w: invalid sha256 for %s", ErrInvalidBundle, entry.Path)
	}
	got, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if string(got) != string(want) {
		return fmt.Errorf("%w: checksum mismatch for %s", ErrInvalidBundle, entry.Path)
	}
	return nil
}

func knownFileKind(kind FileKind) bool {
	switch kind {
	case FileKindMetaUsers,
		FileKindMetaDevices,
		FileKindMetaChannels,
		FileKindMetaSubscribers,
		FileKindMetaUserChannelMemberships,
		FileKindMetaConversations,
		FileKindMetaChannelLatest,
		FileKindMessageChannels,
		FileKindMessageMessages:
		return true
	default:
		return false
	}
}

func safeBundlePath(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: unsafe path %q", ErrInvalidBundle, rel)
	}
	clean := filepath.Clean(rel)
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("%w: unsafe path %q", ErrInvalidBundle, rel)
	}
	return filepath.Join(root, clean), nil
}

func fileSHA256(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: open %s: %v", ErrInvalidBundle, path, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, fmt.Errorf("%w: hash %s: %v", ErrInvalidBundle, path, err)
	}
	return hash.Sum(nil), nil
}
```

- [ ] **Step 5: Run manifest tests and commit**

Run:

```bash
GOWORK=off go test ./pkg/db/transfer -run 'TestLoadManifest' -count=1
```

Expected: pass.

Commit:

```bash
git add pkg/db/transfer
git commit -m "feat(wkdb): add import bundle manifest"
```

---

### Task 2: JSONL Record Decoding

**Files:**
- Modify: `pkg/db/transfer/types.go`
- Create: `pkg/db/transfer/jsonl.go`
- Create: `pkg/db/transfer/jsonl_test.go`

- [ ] **Step 1: Write failing parser tests**

Create `pkg/db/transfer/jsonl_test.go`:

```go
package transfer

import (
	"context"
	"strings"
	"testing"
)

func TestReadJSONLAcceptsUint64NumberAndString(t *testing.T) {
	input := strings.NewReader(
		"{\"channel_key\":\"g1:2\",\"message_seq\":1,\"message_id\":\"18446744073709551615\",\"payload_b64\":\"aGk=\"}\n",
	)
	var got []MessageRecord
	err := readJSONL(context.Background(), input, FileKindMessageMessages, func(record any) error {
		got = append(got, record.(MessageRecord))
		return nil
	})
	if err != nil {
		t.Fatalf("readJSONL() error = %v", err)
	}
	if uint64(got[0].MessageID) != ^uint64(0) || string(got[0].Payload) != "hi" {
		t.Fatalf("record = %+v payload=%q", got[0], string(got[0].Payload))
	}
}

func TestReadJSONLRejectsMissingRequiredField(t *testing.T) {
	input := strings.NewReader("{\"hash_slot\":1,\"token\":\"t1\"}\n")
	err := readJSONL(context.Background(), input, FileKindMetaUsers, func(any) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "uid") {
		t.Fatalf("readJSONL() error = %v, want missing uid", err)
	}
}

func TestReadJSONLRejectsUnknownConversationKind(t *testing.T) {
	input := strings.NewReader("{\"hash_slot\":1,\"uid\":\"u1\",\"kind\":\"other\",\"channel_id\":\"g1\",\"channel_type\":2}\n")
	err := readJSONL(context.Background(), input, FileKindMetaConversations, func(any) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("readJSONL() error = %v, want kind error", err)
	}
}

func TestReadJSONLRejectsBadPayloadBase64(t *testing.T) {
	input := strings.NewReader("{\"channel_key\":\"g1:2\",\"message_seq\":1,\"message_id\":1,\"payload_b64\":\"@@\"}\n")
	err := readJSONL(context.Background(), input, FileKindMessageMessages, func(any) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "payload_b64") {
		t.Fatalf("readJSONL() error = %v, want base64 error", err)
	}
}
```

- [ ] **Step 2: Run parser tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./pkg/db/transfer -run 'TestReadJSONL' -count=1
```

Expected: fail because record types and `readJSONL` do not exist.

- [ ] **Step 3: Add record types**

Append these record types to `pkg/db/transfer/types.go`:

```go
// UserRecord is one metadata user import row.
type UserRecord struct {
	HashSlot    uint16 `json:"hash_slot"`
	UID         string `json:"uid"`
	Token       string `json:"token"`
	DeviceFlag  int64  `json:"device_flag"`
	DeviceLevel int64  `json:"device_level"`
}

// DeviceRecord is one metadata device import row.
type DeviceRecord struct {
	HashSlot    uint16 `json:"hash_slot"`
	UID         string `json:"uid"`
	DeviceFlag  int64  `json:"device_flag"`
	Token       string `json:"token"`
	DeviceLevel int64  `json:"device_level"`
}

// ChannelRecord is one metadata channel import row.
type ChannelRecord struct {
	HashSlot                  uint16 `json:"hash_slot"`
	ChannelID                 string `json:"channel_id"`
	ChannelType               int64  `json:"channel_type"`
	Ban                       int64  `json:"ban"`
	Disband                   int64  `json:"disband"`
	SendBan                   int64  `json:"send_ban"`
	AllowStranger             int64  `json:"allow_stranger"`
	Large                     int64  `json:"large"`
	SubscriberMutationVersion Uint64 `json:"subscriber_mutation_version"`
}

// SubscriberRecord is one metadata subscriber import row.
type SubscriberRecord struct {
	HashSlot    uint16 `json:"hash_slot"`
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
	UID         string `json:"uid"`
}

// UserChannelMembershipRecord is one UID-owned membership import row.
type UserChannelMembershipRecord struct {
	HashSlot    uint16 `json:"hash_slot"`
	UID         string `json:"uid"`
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
	JoinSeq     Uint64 `json:"join_seq"`
	UpdatedAt   int64  `json:"updated_at_ms"`
}

// ConversationRecord is one unified conversation import row.
type ConversationRecord struct {
	HashSlot     uint16 `json:"hash_slot"`
	UID          string `json:"uid"`
	Kind         string `json:"kind"`
	ChannelID    string `json:"channel_id"`
	ChannelType  int64  `json:"channel_type"`
	ReadSeq      Uint64 `json:"read_seq"`
	DeletedToSeq Uint64 `json:"deleted_to_seq"`
	ActiveAt     int64  `json:"active_at"`
	UpdatedAt    int64  `json:"updated_at"`
	SparseActive bool   `json:"sparse_active"`
}

// ChannelLatestRecord is one channel latest projection import row.
type ChannelLatestRecord struct {
	HashSlot              uint16 `json:"hash_slot"`
	ChannelID             string `json:"channel_id"`
	ChannelType           int64  `json:"channel_type"`
	LastMessageID         Uint64 `json:"last_message_id"`
	LastMessageSeq        Uint64 `json:"last_message_seq"`
	LastAt                int64  `json:"last_at"`
	FromUID               string `json:"from_uid"`
	ClientMsgNo           string `json:"client_msg_no"`
	PayloadB64            string `json:"last_payload_b64"`
	Payload               []byte `json:"-"`
	UpdatedAt             int64  `json:"updated_at"`
}

// MessageChannelRecord is one message catalog import row.
type MessageChannelRecord struct {
	ChannelKey  string `json:"channel_key"`
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
}

// MessageRecord is one channel message import row.
type MessageRecord struct {
	ChannelKey        string `json:"channel_key"`
	MessageSeq        Uint64 `json:"message_seq"`
	MessageID         Uint64 `json:"message_id"`
	ClientMsgNo       string `json:"client_msg_no"`
	FromUID           string `json:"from_uid"`
	ServerTimestampMS int64  `json:"server_timestamp_ms"`
	PayloadB64        string `json:"payload_b64"`
	Payload           []byte `json:"-"`
}
```

- [ ] **Step 4: Add JSONL decoder implementation**

Create `pkg/db/transfer/jsonl.go`:

```go
package transfer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

const maxJSONLLineBytes = 64 * 1024 * 1024

func readJSONL(ctx context.Context, r io.Reader, kind FileKind, visit func(any) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxJSONLLineBytes)
	lineNo := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		record, err := decodeRecord(kind, line)
		if err != nil {
			return fmt.Errorf("%w: %s line %d: %v", ErrInvalidBundle, kind, lineNo, err)
		}
		if err := visit(record); err != nil {
			return fmt.Errorf("%s line %d: %w", kind, lineNo, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("%w: scan %s: %v", ErrInvalidBundle, kind, err)
	}
	return nil
}

func decodeRecord(kind FileKind, line []byte) (any, error) {
	switch kind {
	case FileKindMetaUsers:
		var row UserRecord
		if err := decodeStrict(line, &row); err != nil {
			return nil, err
		}
		return row, requireString("uid", row.UID)
	case FileKindMetaDevices:
		var row DeviceRecord
		if err := decodeStrict(line, &row); err != nil {
			return nil, err
		}
		return row, requireString("uid", row.UID)
	case FileKindMetaChannels:
		var row ChannelRecord
		if err := decodeStrict(line, &row); err != nil {
			return nil, err
		}
		return row, requireString("channel_id", row.ChannelID)
	case FileKindMetaSubscribers:
		var row SubscriberRecord
		if err := decodeStrict(line, &row); err != nil {
			return nil, err
		}
		if err := requireString("channel_id", row.ChannelID); err != nil {
			return nil, err
		}
		return row, requireString("uid", row.UID)
	case FileKindMetaUserChannelMemberships:
		var row UserChannelMembershipRecord
		if err := decodeStrict(line, &row); err != nil {
			return nil, err
		}
		if err := requireString("uid", row.UID); err != nil {
			return nil, err
		}
		return row, requireString("channel_id", row.ChannelID)
	case FileKindMetaConversations:
		var row ConversationRecord
		if err := decodeStrict(line, &row); err != nil {
			return nil, err
		}
		if row.Kind != "normal" && row.Kind != "cmd" {
			return nil, fmt.Errorf("kind must be normal or cmd")
		}
		if err := requireString("uid", row.UID); err != nil {
			return nil, err
		}
		return row, requireString("channel_id", row.ChannelID)
	case FileKindMetaChannelLatest:
		var row ChannelLatestRecord
		if err := decodeStrict(line, &row); err != nil {
			return nil, err
		}
		payload, err := decodeBase64Field("last_payload_b64", row.PayloadB64)
		if err != nil {
			return nil, err
		}
		row.Payload = payload
		return row, requireString("channel_id", row.ChannelID)
	case FileKindMessageChannels:
		var row MessageChannelRecord
		if err := decodeStrict(line, &row); err != nil {
			return nil, err
		}
		if err := requireString("channel_key", row.ChannelKey); err != nil {
			return nil, err
		}
		return row, requireString("channel_id", row.ChannelID)
	case FileKindMessageMessages:
		var row MessageRecord
		if err := decodeStrict(line, &row); err != nil {
			return nil, err
		}
		payload, err := decodeBase64Field("payload_b64", row.PayloadB64)
		if err != nil {
			return nil, err
		}
		row.Payload = payload
		if err := requireString("channel_key", row.ChannelKey); err != nil {
			return nil, err
		}
		if uint64(row.MessageSeq) == 0 {
			return nil, fmt.Errorf("message_seq must start at 1")
		}
		if uint64(row.MessageID) == 0 {
			return nil, fmt.Errorf("message_id is required")
		}
		return row, nil
	default:
		return nil, fmt.Errorf("unknown kind %q", kind)
	}
}

func decodeStrict(data []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func decodeBase64Field(name, raw string) ([]byte, error) {
	if raw == "" {
		return []byte{}, nil
	}
	payload, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return payload, nil
}

func requireString(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}

// Uint64 accepts exact JSON numbers or decimal strings for uint64 fields.
type Uint64 uint64

func (u *Uint64) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("empty uint64")
	}
	var text string
	if data[0] == '"' {
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
	} else {
		text = string(data)
	}
	n, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return err
	}
	*u = Uint64(n)
	return nil
}
```

All imported unsigned 64-bit fields use the `Uint64` wrapper so JSON decimal strings and JSON numbers both decode exactly. Convert those values with `uint64(row.Field)` at DB-write boundaries.

- [ ] **Step 5: Run parser tests and commit**

Run:

```bash
GOWORK=off go test ./pkg/db/transfer -run 'TestReadJSONL|TestLoadManifest' -count=1
```

Expected: pass.

Commit:

```bash
git add pkg/db/transfer
git commit -m "feat(wkdb): parse import bundle records"
```

---

### Task 3: Streaming Semantic Validation

**Files:**
- Create: `pkg/db/transfer/validate.go`
- Create: `pkg/db/transfer/validate_test.go`
- Modify: `pkg/db/transfer/types.go`

- [ ] **Step 1: Write failing validation tests**

Create `pkg/db/transfer/validate_test.go`:

```go
package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
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
		`{"hash_slot":1,"channel_id":"b","channel_type":2,"uid":"u1"}`,
		`{"hash_slot":1,"channel_id":"a","channel_type":2,"uid":"u2"}`,
	)
	writeManifestForFiles(t, root, 16, []manifestTestFile{{Path: "meta/subscribers.jsonl", Kind: FileKindMetaSubscribers}})

	_, err := ValidateBundle(context.Background(), root, ImportOptions{HashSlotCount: 16})
	if err == nil || !strings.Contains(err.Error(), "subscriber order") {
		t.Fatalf("ValidateBundle() error = %v, want subscriber order error", err)
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

type manifestTestFile struct {
	Path string
	Kind FileKind
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
		b.WriteString(itoa(strings.Count(string(data), "\n")))
		b.WriteString(`,"sha256":"`)
		b.WriteString(hex.EncodeToString(sum[:]))
		b.WriteString(`"}`)
	}
	b.WriteString(`]}`)
	writeManifest(t, root, b.String())
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
```

Add `strconv` to the imports in this test file.

- [ ] **Step 2: Run validation tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./pkg/db/transfer -run 'TestValidateBundle' -count=1
```

Expected: fail because `ValidateBundle` does not exist.

- [ ] **Step 3: Add validation implementation**

Create `pkg/db/transfer/validate.go`:

```go
package transfer

import (
	"context"
	"fmt"
	"os"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// ValidateBundle validates a bundle without writing target storage.
func ValidateBundle(ctx context.Context, root string, opts ImportOptions) (ImportStats, error) {
	manifest, err := LoadManifest(root)
	if err != nil {
		return ImportStats{}, err
	}
	if opts.HashSlotCount == 0 {
		opts.HashSlotCount = manifest.HashSlotCount
	}
	if opts.HashSlotCount == 0 || opts.HashSlotCount != manifest.HashSlotCount {
		return ImportStats{}, fmt.Errorf("%w: hash-slot count mismatch manifest=%d target=%d", ErrInvalidBundle, manifest.HashSlotCount, opts.HashSlotCount)
	}
	validator := newBundleValidator(opts.HashSlotCount)
	stats := ImportStats{Files: len(manifest.Files)}
	for _, entry := range manifest.Files {
		path, err := safeBundlePath(root, entry.Path)
		if err != nil {
			return stats, err
		}
		if info, err := os.Stat(path); err == nil {
			stats.BytesRead += info.Size()
		}
		file, err := os.Open(path)
		if err != nil {
			return stats, fmt.Errorf("%w: open %s: %v", ErrInvalidBundle, entry.Path, err)
		}
		err = readJSONL(ctx, file, entry.Kind, func(record any) error {
			stats.RowsValidated++
			return validator.Visit(entry.Kind, record)
		})
		closeErr := file.Close()
		if err != nil {
			return stats, err
		}
		if closeErr != nil {
			return stats, closeErr
		}
	}
	if err := validator.Close(); err != nil {
		return stats, err
	}
	return stats, nil
}

type bundleValidator struct {
	hashSlotCount uint16
	channels      map[string]MessageChannelRecord
	subscribers   subscriberOrder
	messages      messageOrder
}

func newBundleValidator(hashSlotCount uint16) *bundleValidator {
	return &bundleValidator{
		hashSlotCount: hashSlotCount,
		channels:      make(map[string]MessageChannelRecord),
	}
}

func (v *bundleValidator) Visit(kind FileKind, record any) error {
	switch kind {
	case FileKindMetaUsers:
		row := record.(UserRecord)
		return v.checkHashSlot(row.HashSlot, row.UID)
	case FileKindMetaDevices:
		row := record.(DeviceRecord)
		return v.checkHashSlot(row.HashSlot, row.UID)
	case FileKindMetaChannels:
		row := record.(ChannelRecord)
		return v.checkHashSlot(row.HashSlot, row.ChannelID)
	case FileKindMetaSubscribers:
		row := record.(SubscriberRecord)
		if err := v.checkHashSlot(row.HashSlot, row.ChannelID); err != nil {
			return err
		}
		return v.subscribers.Visit(row)
	case FileKindMetaUserChannelMemberships:
		row := record.(UserChannelMembershipRecord)
		return v.checkHashSlot(row.HashSlot, row.UID)
	case FileKindMetaConversations:
		row := record.(ConversationRecord)
		return v.checkHashSlot(row.HashSlot, row.UID)
	case FileKindMetaChannelLatest:
		row := record.(ChannelLatestRecord)
		return v.checkHashSlot(row.HashSlot, row.ChannelID)
	case FileKindMessageChannels:
		row := record.(MessageChannelRecord)
		if _, exists := v.channels[row.ChannelKey]; exists {
			return fmt.Errorf("%w: duplicate channel_key %s", ErrValidation, row.ChannelKey)
		}
		v.channels[row.ChannelKey] = row
		return nil
	case FileKindMessageMessages:
		row := record.(MessageRecord)
		if _, ok := v.channels[row.ChannelKey]; !ok {
			return fmt.Errorf("%w: missing message channel %s", ErrValidation, row.ChannelKey)
		}
		return v.messages.Visit(row)
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidBundle, kind)
	}
}

func (v *bundleValidator) Close() error { return nil }

func (v *bundleValidator) checkHashSlot(got uint16, key string) error {
	want := cluster.HashSlotForKey(key, v.hashSlotCount)
	if got != want {
		return fmt.Errorf("%w: hash_slot mismatch key=%q got=%d want=%d", ErrValidation, key, got, want)
	}
	return nil
}

type subscriberOrder struct {
	lastSet bool
	last    SubscriberRecord
}

func (o *subscriberOrder) Visit(row SubscriberRecord) error {
	if !o.lastSet {
		o.last = row
		o.lastSet = true
		return nil
	}
	if compareSubscriberOrder(o.last, row) > 0 {
		return fmt.Errorf("%w: subscriber order must be hash_slot, channel_id, channel_type, uid", ErrValidation)
	}
	o.last = row
	return nil
}

func compareSubscriberOrder(a, b SubscriberRecord) int {
	if a.HashSlot != b.HashSlot {
		if a.HashSlot < b.HashSlot {
			return -1
		}
		return 1
	}
	if a.ChannelID != b.ChannelID {
		if a.ChannelID < b.ChannelID {
			return -1
		}
		return 1
	}
	if a.ChannelType != b.ChannelType {
		if a.ChannelType < b.ChannelType {
			return -1
		}
		return 1
	}
	if a.UID < b.UID {
		return -1
	}
	if a.UID > b.UID {
		return 1
	}
	return 0
}

type messageOrder struct {
	currentChannel string
	nextSeq        uint64
	messageIDs     map[uint64]struct{}
	idempotency    map[string]struct{}
}

func (o *messageOrder) Visit(row MessageRecord) error {
	if row.ChannelKey != o.currentChannel {
		if o.currentChannel != "" && row.ChannelKey < o.currentChannel {
			return fmt.Errorf("%w: message rows must be ordered by channel_key", ErrValidation)
		}
		o.currentChannel = row.ChannelKey
		o.nextSeq = 1
		o.messageIDs = make(map[uint64]struct{})
		o.idempotency = make(map[string]struct{})
	}
	if uint64(row.MessageSeq) != o.nextSeq {
		return fmt.Errorf("%w: message_seq must be contiguous for %s got=%d want=%d", ErrValidation, row.ChannelKey, row.MessageSeq, o.nextSeq)
	}
	o.nextSeq++
	messageID := uint64(row.MessageID)
	if _, exists := o.messageIDs[messageID]; exists {
		return fmt.Errorf("%w: duplicate message_id %d in %s", ErrValidation, messageID, row.ChannelKey)
	}
	o.messageIDs[messageID] = struct{}{}
	if row.FromUID != "" && row.ClientMsgNo != "" {
		key := row.FromUID + "\x00" + row.ClientMsgNo
		if _, exists := o.idempotency[key]; exists {
			return fmt.Errorf("%w: duplicate idempotency key in %s", ErrValidation, row.ChannelKey)
		}
		o.idempotency[key] = struct{}{}
	}
	return nil
}
```

- [ ] **Step 4: Fix the test helper imports**

Ensure `pkg/db/transfer/validate_test.go` imports `strconv` because `itoa` uses it:

```go
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
```

- [ ] **Step 5: Run validation tests and commit**

Run:

```bash
GOWORK=off go test ./pkg/db/transfer -run 'TestValidateBundle|TestReadJSONL|TestLoadManifest' -count=1
```

Expected: pass.

Commit:

```bash
git add pkg/db/transfer
git commit -m "feat(wkdb): validate import bundles"
```

---

### Task 4: Typed Import Into Current Storage

**Files:**
- Create: `pkg/db/transfer/importer.go`
- Create: `pkg/db/transfer/importer_test.go`
- Modify: `pkg/db/transfer/validate.go`

- [ ] **Step 1: Write failing real import tests**

Create `pkg/db/transfer/importer_test.go`:

```go
package transfer

import (
	"context"
	"path/filepath"
	"testing"

	db "github.com/WuKongIM/WuKongIM/pkg/db"
	msgdb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestImportBundleWritesCurrentStores(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	hashSlotCount := uint16(16)
	userSlot := testHashSlot("u1", hashSlotCount)
	channelSlot := testHashSlot("g1", hashSlotCount)
	writeJSONLFile(t, root, "meta/users.jsonl", `{"hash_slot":`+itoa(int(userSlot))+`,"uid":"u1","token":"t1","device_flag":1,"device_level":2}`)
	writeJSONLFile(t, root, "meta/devices.jsonl", `{"hash_slot":`+itoa(int(userSlot))+`,"uid":"u1","device_flag":1,"token":"dt1","device_level":2}`)
	writeJSONLFile(t, root, "meta/channels.jsonl", `{"hash_slot":`+itoa(int(channelSlot))+`,"channel_id":"g1","channel_type":2,"allow_stranger":1,"large":1,"subscriber_mutation_version":7}`)
	writeJSONLFile(t, root, "meta/subscribers.jsonl", `{"hash_slot":`+itoa(int(channelSlot))+`,"channel_id":"g1","channel_type":2,"uid":"u1"}`)
	writeJSONLFile(t, root, "meta/user_channel_memberships.jsonl", `{"hash_slot":`+itoa(int(userSlot))+`,"uid":"u1","channel_id":"g1","channel_type":2,"join_seq":1,"updated_at_ms":11}`)
	writeJSONLFile(t, root, "meta/conversations.jsonl", `{"hash_slot":`+itoa(int(userSlot))+`,"uid":"u1","kind":"normal","channel_id":"g1","channel_type":2,"read_seq":1,"deleted_to_seq":0,"active_at":10,"updated_at":11,"sparse_active":true}`)
	writeJSONLFile(t, root, "meta/channel_latest.jsonl", `{"hash_slot":`+itoa(int(channelSlot))+`,"channel_id":"g1","channel_type":2,"last_message_id":1001,"last_message_seq":1,"last_at":10,"from_uid":"u1","client_msg_no":"c1","last_payload_b64":"aGk=","updated_at":11}`)
	writeJSONLFile(t, root, "message/channels.jsonl", `{"channel_key":"g1:2","channel_id":"g1","channel_type":2}`)
	writeJSONLFile(t, root, "message/messages-000001.jsonl", `{"channel_key":"g1:2","message_seq":1,"message_id":1001,"client_msg_no":"c1","from_uid":"u1","server_timestamp_ms":10,"payload_b64":"aGk="}`)
	writeManifestForFiles(t, root, hashSlotCount, []manifestTestFile{
		{Path: "meta/users.jsonl", Kind: FileKindMetaUsers},
		{Path: "meta/devices.jsonl", Kind: FileKindMetaDevices},
		{Path: "meta/channels.jsonl", Kind: FileKindMetaChannels},
		{Path: "meta/subscribers.jsonl", Kind: FileKindMetaSubscribers},
		{Path: "meta/user_channel_memberships.jsonl", Kind: FileKindMetaUserChannelMemberships},
		{Path: "meta/conversations.jsonl", Kind: FileKindMetaConversations},
		{Path: "meta/channel_latest.jsonl", Kind: FileKindMetaChannelLatest},
		{Path: "message/channels.jsonl", Kind: FileKindMessageChannels},
		{Path: "message/messages-000001.jsonl", Kind: FileKindMessageMessages},
	})

	store := openTransferNodeStore(t)
	stats, err := ImportBundle(ctx, root, store, ImportOptions{HashSlotCount: hashSlotCount, RequireEmpty: true})
	if err != nil {
		t.Fatalf("ImportBundle() error = %v", err)
	}
	if stats.MessagesImported != 1 || stats.SubscribersImported != 1 {
		t.Fatalf("stats = %+v, want one message and one subscriber", stats)
	}

	meta := store.Meta()
	user, ok, err := meta.HashSlot(userSlot).GetUser(ctx, "u1")
	if err != nil || !ok || user.Token != "t1" {
		t.Fatalf("GetUser() = %+v ok=%v err=%v", user, ok, err)
	}
	channel, ok, err := meta.HashSlot(channelSlot).GetChannel(ctx, "g1", 2)
	if err != nil || !ok || channel.SubscriberCount != 1 {
		t.Fatalf("GetChannel() = %+v ok=%v err=%v, want subscriber count 1", channel, ok, err)
	}
	conv, ok, err := meta.HashSlot(userSlot).GetConversationState(ctx, metadb.ConversationKindNormal, "u1", "g1", 2)
	if err != nil || !ok || conv.ReadSeq != 1 || !conv.SparseActive {
		t.Fatalf("GetConversationState() = %+v ok=%v err=%v", conv, ok, err)
	}

	log := store.Messages().Channel(msgdb.ChannelKey("g1:2"), msgdb.ChannelID{ID: "g1", Type: 2})
	msgs, err := log.Read(ctx, 1, msgdb.ReadOptions{Limit: 1})
	if err != nil || len(msgs) != 1 || string(msgs[0].Payload) != "hi" {
		t.Fatalf("Read() = %+v err=%v", msgs, err)
	}
}

func TestImportBundleRejectsNonEmptyTarget(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	hashSlotCount := uint16(16)
	writeManifestForFiles(t, root, hashSlotCount, nil)

	store := openTransferNodeStore(t)
	slot := testHashSlot("u1", hashSlotCount)
	if err := store.Meta().HashSlot(slot).UpsertUser(ctx, metadb.User{UID: "u1", Token: "existing"}); err != nil {
		t.Fatalf("UpsertUser(): %v", err)
	}

	_, err := ImportBundle(ctx, root, store, ImportOptions{HashSlotCount: hashSlotCount, RequireEmpty: true})
	if err == nil || !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("ImportBundle() error = %v, want non-empty target", err)
	}
}

func openTransferNodeStore(t *testing.T) *db.NodeStore {
	t.Helper()
	dir := t.TempDir()
	store, err := db.OpenNodeStore(db.NodeStoreOptions{
		MetaPath:    filepath.Join(dir, "data"),
		MessagePath: filepath.Join(dir, "channellog"),
	})
	if err != nil {
		t.Fatalf("OpenNodeStore(): %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	})
	return store
}
```

Add these imports to `pkg/db/transfer/importer_test.go`: `strings` and `github.com/WuKongIM/WuKongIM/pkg/cluster`. Add helper:

```go
func testHashSlot(key string, count uint16) uint16 {
	return cluster.HashSlotForKey(key, count)
}
```

- [ ] **Step 2: Run import tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./pkg/db/transfer -run 'TestImportBundle' -count=1
```

Expected: fail because `ImportBundle` does not exist.

- [ ] **Step 3: Add importer implementation**

Create `pkg/db/transfer/importer.go`:

```go
package transfer

import (
	"context"
	"fmt"
	"os"

	db "github.com/WuKongIM/WuKongIM/pkg/db"
	msgdb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	defaultSubscriberBatchSize = 1024
	defaultMessageBatchSize    = 1024
	defaultMessageBatchBytes   = 4 << 20
)

// ImportBundle validates and imports a bundle into the target store.
func ImportBundle(ctx context.Context, root string, store *db.NodeStore, opts ImportOptions) (ImportStats, error) {
	if store == nil {
		return ImportStats{}, fmt.Errorf("%w: nil target store", ErrInvalidBundle)
	}
	opts = normalizeImportOptions(opts)
	stats, err := ValidateBundle(ctx, root, opts)
	if err != nil {
		return stats, err
	}
	if opts.RequireEmpty {
		if err := checkTargetEmpty(ctx, store, opts.HashSlotCount); err != nil {
			return stats, err
		}
	}
	manifest, err := LoadManifest(root)
	if err != nil {
		return stats, err
	}
	writer := newImportWriter(store, opts)
	for _, entry := range manifest.Files {
		path, err := safeBundlePath(root, entry.Path)
		if err != nil {
			return stats, err
		}
		file, err := os.Open(path)
		if err != nil {
			return stats, err
		}
		err = readJSONL(ctx, file, entry.Kind, func(record any) error {
			if err := writer.Visit(ctx, entry.Kind, record); err != nil {
				return err
			}
			stats.RowsWritten++
			return nil
		})
		closeErr := file.Close()
		if err != nil {
			return stats, err
		}
		if closeErr != nil {
			return stats, closeErr
		}
	}
	if err := writer.Close(ctx); err != nil {
		return stats, err
	}
	stats.ChannelsImported = writer.channelsImported
	stats.MessagesImported = writer.messagesImported
	stats.SubscribersImported = writer.subscribersImported
	return stats, nil
}

func normalizeImportOptions(opts ImportOptions) ImportOptions {
	if opts.SubscriberBatchSize <= 0 {
		opts.SubscriberBatchSize = defaultSubscriberBatchSize
	}
	if opts.MessageBatchSize <= 0 {
		opts.MessageBatchSize = defaultMessageBatchSize
	}
	if opts.MessageBatchBytes <= 0 {
		opts.MessageBatchBytes = defaultMessageBatchBytes
	}
	return opts
}

func checkTargetEmpty(ctx context.Context, store *db.NodeStore, hashSlotCount uint16) error {
	for _, table := range metadb.InspectTables() {
		scan, err := metadb.InspectScan(ctx, store.Meta(), metadb.InspectScanRequest{
			Table:         table.Name,
			HashSlotCount: hashSlotCount,
			Limit:         1,
		})
		if err != nil {
			return err
		}
		if len(scan.Rows) > 0 {
			return fmt.Errorf("%w: target store is non-empty: meta.%s", ErrValidation, table.Name)
		}
	}
	channels, err := msgdb.InspectChannels(ctx, store.Messages(), msgdb.InspectMessageRequest{Limit: 1})
	if err != nil {
		return err
	}
	if len(channels.Rows) > 0 {
		return fmt.Errorf("%w: target store is non-empty: message.channels", ErrValidation)
	}
	return nil
}

type importWriter struct {
	store               *db.NodeStore
	opts                ImportOptions
	messageChannels     map[string]msgdb.ChannelID
	subscriberGroup     subscriberGroup
	messageGroup        messageGroup
	channelsImported    int64
	messagesImported    int64
	subscribersImported int64
}

func newImportWriter(store *db.NodeStore, opts ImportOptions) *importWriter {
	w := &importWriter{
		store:           store,
		opts:            opts,
		messageChannels: make(map[string]msgdb.ChannelID),
	}
	w.subscriberGroup.opts = opts
	w.messageGroup.opts = opts
	return w
}

func (w *importWriter) Visit(ctx context.Context, kind FileKind, record any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch kind {
	case FileKindMetaUsers:
		row := record.(UserRecord)
		return w.store.Meta().HashSlot(row.HashSlot).UpsertUser(ctx, metadb.User{UID: row.UID, Token: row.Token, DeviceFlag: row.DeviceFlag, DeviceLevel: row.DeviceLevel})
	case FileKindMetaDevices:
		row := record.(DeviceRecord)
		return w.store.Meta().HashSlot(row.HashSlot).UpsertDevice(ctx, metadb.Device{UID: row.UID, DeviceFlag: row.DeviceFlag, Token: row.Token, DeviceLevel: row.DeviceLevel})
	case FileKindMetaChannels:
		row := record.(ChannelRecord)
		return w.store.Meta().HashSlot(row.HashSlot).UpsertChannel(ctx, metadb.Channel{ChannelID: row.ChannelID, ChannelType: row.ChannelType, Ban: row.Ban, Disband: row.Disband, SendBan: row.SendBan, AllowStranger: row.AllowStranger, Large: row.Large, SubscriberMutationVersion: uint64(row.SubscriberMutationVersion)})
	case FileKindMetaSubscribers:
		return w.subscriberGroup.Visit(ctx, w.store.Meta(), record.(SubscriberRecord), &w.subscribersImported)
	case FileKindMetaUserChannelMemberships:
		row := record.(UserChannelMembershipRecord)
		return w.store.Meta().HashSlot(row.HashSlot).UpsertUserChannelMembership(ctx, metadb.UserChannelMembership{UID: row.UID, ChannelID: row.ChannelID, ChannelType: row.ChannelType, JoinSeq: uint64(row.JoinSeq), UpdatedAt: row.UpdatedAt})
	case FileKindMetaConversations:
		row := record.(ConversationRecord)
		return w.store.Meta().HashSlot(row.HashSlot).UpsertConversationState(ctx, metadb.ConversationState{UID: row.UID, Kind: conversationKind(row.Kind), ChannelID: row.ChannelID, ChannelType: row.ChannelType, ReadSeq: uint64(row.ReadSeq), DeletedToSeq: uint64(row.DeletedToSeq), ActiveAt: row.ActiveAt, UpdatedAt: row.UpdatedAt, SparseActive: row.SparseActive})
	case FileKindMetaChannelLatest:
		row := record.(ChannelLatestRecord)
		return w.store.Meta().HashSlot(row.HashSlot).UpsertChannelLatest(ctx, metadb.ChannelLatest{ChannelID: row.ChannelID, ChannelType: row.ChannelType, LastMessageID: uint64(row.LastMessageID), LastMessageSeq: uint64(row.LastMessageSeq), LastAt: row.LastAt, FromUID: row.FromUID, ClientMsgNo: row.ClientMsgNo, Payload: row.Payload, UpdatedAt: row.UpdatedAt})
	case FileKindMessageChannels:
		row := record.(MessageChannelRecord)
		w.messageChannels[row.ChannelKey] = msgdb.ChannelID{ID: row.ChannelID, Type: row.ChannelType}
		w.channelsImported++
		return nil
	case FileKindMessageMessages:
		row := record.(MessageRecord)
		id, ok := w.messageChannels[row.ChannelKey]
		if !ok {
			return fmt.Errorf("%w: missing message channel %s", ErrValidation, row.ChannelKey)
		}
		return w.messageGroup.Visit(ctx, w.store.Messages(), id, row, &w.messagesImported)
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidBundle, kind)
	}
}

func (w *importWriter) Close(ctx context.Context) error {
	if err := w.subscriberGroup.Flush(ctx, w.store.Meta(), &w.subscribersImported); err != nil {
		return err
	}
	return w.messageGroup.Flush(ctx, w.store.Messages(), &w.messagesImported)
}

func conversationKind(kind string) metadb.ConversationKind {
	if kind == "cmd" {
		return metadb.ConversationKindCMD
	}
	return metadb.ConversationKindNormal
}
```

Add subscriber and message group helpers below in the same file:

```go
type subscriberGroup struct {
	opts        ImportOptions
	set         bool
	hashSlot    uint16
	channelID   string
	channelType int64
	uids        []string
}

func (g *subscriberGroup) Visit(ctx context.Context, meta *metadb.MetaDB, row SubscriberRecord, count *int64) error {
	if !g.set || row.HashSlot != g.hashSlot || row.ChannelID != g.channelID || row.ChannelType != g.channelType {
		if err := g.Flush(ctx, meta, count); err != nil {
			return err
		}
		g.set = true
		g.hashSlot = row.HashSlot
		g.channelID = row.ChannelID
		g.channelType = row.ChannelType
	}
	g.uids = append(g.uids, row.UID)
	if len(g.uids) >= g.opts.SubscriberBatchSize {
		return g.Flush(ctx, meta, count)
	}
	return nil
}

func (g *subscriberGroup) Flush(ctx context.Context, meta *metadb.MetaDB, count *int64) error {
	if !g.set || len(g.uids) == 0 {
		return nil
	}
	if err := meta.HashSlot(g.hashSlot).AddSubscribers(ctx, g.channelID, g.channelType, g.uids, 0); err != nil {
		return err
	}
	*count += int64(len(g.uids))
	g.uids = g.uids[:0]
	return nil
}

type messageGroup struct {
	opts       ImportOptions
	channelKey string
	channelID  msgdb.ChannelID
	records    []msgdb.Record
	bytes      int
}

func (g *messageGroup) Visit(ctx context.Context, messages *msgdb.MessageDB, id msgdb.ChannelID, row MessageRecord, count *int64) error {
	if g.channelKey == "" {
		g.channelKey = row.ChannelKey
		g.channelID = id
	} else if row.ChannelKey != g.channelKey {
		if err := g.Flush(ctx, messages, count); err != nil {
			return err
		}
		g.channelKey = row.ChannelKey
		g.channelID = id
	}
	g.records = append(g.records, msgdb.Record{ID: uint64(row.MessageID), ClientMsgNo: row.ClientMsgNo, FromUID: row.FromUID, Payload: row.Payload, SizeBytes: len(row.Payload), ServerTimestampMS: row.ServerTimestampMS})
	g.bytes += len(row.Payload)
	if len(g.records) >= g.opts.MessageBatchSize || g.bytes >= g.opts.MessageBatchBytes {
		return g.Flush(ctx, messages, count)
	}
	return nil
}

func (g *messageGroup) Flush(ctx context.Context, messages *msgdb.MessageDB, count *int64) error {
	if g.channelKey == "" || len(g.records) == 0 {
		return nil
	}
	log := messages.Channel(msgdb.ChannelKey(g.channelKey), g.channelID)
	baseSeq := uint64(0)
	if leo, err := log.LEO(ctx); err == nil {
		baseSeq = leo + 1
	} else {
		return err
	}
	result, err := log.ApplyFetch(ctx, msgdb.ApplyFetchRequest{BaseSeq: baseSeq, Records: g.records})
	if err != nil {
		return err
	}
	*count += int64(result.Count)
	g.records = g.records[:0]
	g.bytes = 0
	return nil
}
```

- [ ] **Step 4: Verify typed field conversions**

Confirm every assignment from transfer `Uint64` fields to current DB structs converts explicitly:

```go
LastMessageID:  uint64(row.LastMessageID),
LastMessageSeq: uint64(row.LastMessageSeq),
ReadSeq:        uint64(row.ReadSeq),
DeletedToSeq:   uint64(row.DeletedToSeq),
MessageID:      uint64(row.MessageID),
```

Run `gofmt` after these conversions.

- [ ] **Step 5: Run import tests and commit**

Run:

```bash
GOWORK=off go test ./pkg/db/transfer -count=1
```

Expected: pass.

Commit:

```bash
git add pkg/db/transfer
git commit -m "feat(wkdb): import bundles into node store"
```

---

### Task 5: `wkdb import` CLI Integration

**Files:**
- Create: `cmd/wkdb/import.go`
- Modify: `cmd/wkdb/main.go`
- Modify: `cmd/wkdb/config.go`
- Modify: `cmd/wkdb/main_test.go`

- [ ] **Step 1: Write failing CLI tests**

Append to `cmd/wkdb/main_test.go`:

```go
func TestRunImportDryRunAcceptsCommandLocalFlags(t *testing.T) {
	root := t.TempDir()
	writeWkdbJSONLFile(t, root, "message/channels.jsonl", "")
	writeWkdbManifestForFiles(t, root, 16, []wkdbManifestTestFile{{Path: "message/channels.jsonl", Kind: transfer.FileKindMessageChannels}})

	var stderr bytes.Buffer
	code := runWithIO([]string{"import", "--input", root, "--hash-slot-count", "16", "--dry-run"}, nil, &stderr)
	if code != exitOK {
		t.Fatalf("exit code = %d stderr=%q", code, stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("validated")) {
		t.Fatalf("stderr = %q, want validated summary", stderr.String())
	}
}

func TestRunImportRequiresInput(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithIO([]string{"import", "--hash-slot-count", "16", "--dry-run"}, nil, &stderr)
	if code != exitConfig {
		t.Fatalf("exit code = %d, want %d", code, exitConfig)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("--input")) {
		t.Fatalf("stderr = %q, want input error", stderr.String())
	}
}

type wkdbManifestTestFile struct {
	Path string
	Kind transfer.FileKind
}

func writeWkdbJSONLFile(t *testing.T, root, rel string, lines ...string) {
	t.Helper()
	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func writeWkdbManifestForFiles(t *testing.T, root string, hashSlotCount uint16, files []wkdbManifestTestFile) {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"format":"wkdb-import-bundle","version":1,"hash_slot_count":`)
	b.WriteString(strconv.Itoa(int(hashSlotCount)))
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
		b.WriteString(strconv.Itoa(strings.Count(string(data), "\n")))
		b.WriteString(`,"sha256":"`)
		b.WriteString(hex.EncodeToString(sum[:]))
		b.WriteString(`"}`)
	}
	b.WriteString(`]}`)
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(b.String()), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest.json): %v", err)
	}
}
```

Add required imports to `cmd/wkdb/main_test.go`:

```go
import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)
```

- [ ] **Step 2: Run CLI tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./cmd/wkdb -run 'TestRunImport' -count=1
```

Expected: fail because `import` is an unknown command.

- [ ] **Step 3: Add import command implementation**

Create `cmd/wkdb/import.go`:

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	db "github.com/WuKongIM/WuKongIM/pkg/db"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

func parseImportFlags(base cliFlags, args []string, stderr io.Writer) (cliFlags, int) {
	flags := base
	var hashSlotCount uint
	fs := flag.NewFlagSet("wkdb import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&flags.configPath, "config", flags.configPath, "path to wukongim.conf")
	fs.StringVar(&flags.dataDir, "data-dir", flags.dataDir, "node data directory")
	fs.StringVar(&flags.metaPath, "meta-path", flags.metaPath, "metadata store path")
	fs.StringVar(&flags.messagePath, "message-path", flags.messagePath, "message store path")
	fs.UintVar(&hashSlotCount, "hash-slot-count", uint(flags.hashSlotCount), "cluster hash slot count")
	fs.StringVar(&flags.importInput, "input", flags.importInput, "WKDB import bundle directory")
	fs.BoolVar(&flags.importDryRun, "dry-run", flags.importDryRun, "validate import bundle without writing")
	fs.BoolVar(&flags.importRequireEmpty, "require-empty", flags.importRequireEmpty, "require an empty target store")
	if err := fs.Parse(args); err != nil {
		return cliFlags{}, exitConfig
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected import arguments: %v\n", fs.Args())
		return cliFlags{}, exitConfig
	}
	if hashSlotCount > 65535 {
		fmt.Fprintln(stderr, "--hash-slot-count must be <= 65535")
		return cliFlags{}, exitConfig
	}
	flags.hashSlotCount = uint16(hashSlotCount)
	return flags, exitOK
}

func runImport(ctx context.Context, flags cliFlags, stderr io.Writer) int {
	if flags.importInput == "" {
		fmt.Fprintln(stderr, "config error: --input is required")
		return exitConfig
	}
	cfg, err := resolveCLIConfig(flags, os.Environ())
	if err != nil {
		fmt.Fprintf(stderr, "config error: %v\n", err)
		return exitConfig
	}
	opts := transfer.ImportOptions{
		HashSlotCount: cfg.options.HashSlotCount,
		RequireEmpty:  flags.importRequireEmpty,
		DryRun:        flags.importDryRun,
	}
	if flags.importDryRun {
		stats, err := transfer.ValidateBundle(ctx, flags.importInput, opts)
		if err != nil {
			fmt.Fprintf(stderr, "import validation error: %v\n", err)
			return exitImport
		}
		fmt.Fprintf(stderr, "validated files=%d rows=%d bytes=%d\n", stats.Files, stats.RowsValidated, stats.BytesRead)
		return exitOK
	}
	if !flags.importRequireEmpty {
		fmt.Fprintln(stderr, "config error: --require-empty is required for real import")
		return exitConfig
	}
	store, err := db.OpenNodeStore(db.NodeStoreOptions{
		MetaPath:    cfg.options.MetaPath,
		MessagePath: cfg.options.MessagePath,
	})
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return exitConfig
	}
	defer store.Close()
	stats, err := transfer.ImportBundle(ctx, flags.importInput, store, opts)
	if err != nil {
		fmt.Fprintf(stderr, "import error: %v\n", err)
		return exitImport
	}
	fmt.Fprintf(stderr, "imported files=%d rows=%d channels=%d messages=%d subscribers=%d\n", stats.Files, stats.RowsWritten, stats.ChannelsImported, stats.MessagesImported, stats.SubscribersImported)
	return exitOK
}
```

- [ ] **Step 4: Wire `main.go` and config flags**

Modify `cmd/wkdb/config.go`:

```go
type cliFlags struct {
	// existing fields...
	// importInput points to a WKDB import bundle directory.
	importInput string
	// importDryRun validates the import bundle without writing.
	importDryRun bool
	// importRequireEmpty requires an empty target before real import.
	importRequireEmpty bool
}
```

Modify `cmd/wkdb/main.go`:

```go
const (
	exitOK       = 0
	exitConfig   = 1
	exitQuery    = 2
	exitInternal = 3
	exitImport   = 4
)
```

In `runWithIO`, change the usage string to include import:

```go
fmt.Fprintln(stderr, "usage: wkdb [flags] <query|repl|import>")
```

Add the import switch case:

```go
case "import":
	importFlags, code := parseImportFlags(flags, rest[1:], stderr)
	if code != exitOK {
		return code
	}
	return runImport(context.Background(), importFlags, stderr)
```

- [ ] **Step 5: Run CLI tests and commit**

Run:

```bash
GOWORK=off go test ./cmd/wkdb -run 'TestRunImport|TestRunQueryShowTables|TestRunRejectsUnknownCommand' -count=1
```

Expected: pass.

Commit:

```bash
git add cmd/wkdb pkg/db/transfer
git commit -m "feat(wkdb): add import command"
```

---

### Task 6: Documentation, Full Tests, And Plan Verification

**Files:**
- Modify: `cmd/wkdb/README.md`
- Modify: `docs/wiki/operations/wkdb-readonly-cli.md`
- Modify: `docs/superpowers/specs/2026-06-22-wkdb-import-bundle-design.md` only if implementation discovered a required contract correction

- [ ] **Step 1: Update `cmd/wkdb/README.md`**

Change the opening description from read-only-only wording to explicit command-specific wording:

```markdown
`wkdb query` and `wkdb repl` are read-only offline inspection commands for one WuKongIM node data directory.
`wkdb import` is an explicit offline write command that imports a WKDB Import Bundle into a stopped or fresh node data directory.
```

Add examples:

```markdown
wkdb import --input ./wkdb-dump --hash-slot-count 256 --dry-run
wkdb import --data-dir ./node-new --input ./wkdb-dump --hash-slot-count 256 --require-empty
```

Add the import safety note:

```markdown
Real import requires `--require-empty` and rejects non-empty metadata or message stores. Import into a fresh offline data directory, validate with `wkdb query`, then start the node with that directory.
```

- [ ] **Step 2: Update operations document**

In `docs/wiki/operations/wkdb-readonly-cli.md`, keep the existing read-only sections for `query` and `repl`. Add a new section near the end:

```markdown
## 数据导入：wkdb import

`wkdb import` 是显式离线写入命令，不属于只读排查路径。它只读取 `WKDB Import Bundle v1`，不识别任意旧版本数据库格式。

推荐流程：

1. 让旧版本按 `manifest.json + JSONL` bundle 格式导出。
2. 对 bundle 执行 dry-run：

```bash
wkdb import --input ./wkdb-dump --hash-slot-count 256 --dry-run
```

3. 准备一个新的离线数据目录。
4. 执行真实导入：

```bash
wkdb import --data-dir ./node-new --input ./wkdb-dump --hash-slot-count 256 --require-empty
```

5. 用 `wkdb query` 核对 user、channel、subscriber、conversation、message 数据。
6. 确认后再让节点使用新目录启动。

真实导入不是整库事务。如果导入中途失败，目标目录视为部分导入，必须丢弃或从备份恢复后重试。
```

Add a compact bundle layout and mention these ordering rules:

```markdown
- `meta/subscribers.jsonl` 按 `hash_slot, channel_id, channel_type, uid` 升序排列。
- `message/messages-*.jsonl` 按 `channel_key, message_seq` 全局升序排列。
- `hash_slot` 使用当前 `CRC32(key) % hash_slot_count` 规则，User/Device/Conversation/Membership 用 `uid`，Channel/Subscriber/ChannelLatest 用 `channel_id`。
```

- [ ] **Step 3: Run focused package tests**

Run:

```bash
GOWORK=off go test ./pkg/db/transfer ./cmd/wkdb -count=1
```

Expected: pass.

- [ ] **Step 4: Run DB regression tests**

Run:

```bash
GOWORK=off go test ./pkg/db/... -count=1
```

Expected: pass.

- [ ] **Step 5: Run final targeted command from the spec**

Run:

```bash
GOWORK=off go test ./cmd/wkdb ./pkg/db/transfer ./pkg/db/... -count=1
```

Expected: pass.

- [ ] **Step 6: Commit docs and verification-ready state**

Commit:

```bash
git add cmd/wkdb/README.md docs/wiki/operations/wkdb-readonly-cli.md docs/superpowers/specs/2026-06-22-wkdb-import-bundle-design.md
git commit -m "docs(wkdb): document import bundle workflow"
```

If the spec file did not change, omit it from `git add`.

---

## Self Review

- Spec coverage:
  - Bundle manifest and file contract: Task 1.
  - JSONL record contracts and base64 payloads: Task 2.
  - Hash-slot, ordering, duplicate, and catalog validation: Task 3.
  - Typed writes through current metadata and message APIs: Task 4.
  - Explicit `wkdb import`, dry-run, and empty-target safety: Task 5.
  - README and operations documentation: Task 6.
- Scope stays focused on offline import. It does not add old-storage readers, online import, merge semantics, Raft/control-state import, or raw Pebble writes.
- Type consistency:
  - Transfer records map directly to `metadb.User`, `metadb.Device`, `metadb.Channel`, `metadb.UserChannelMembership`, `metadb.ConversationState`, `metadb.ChannelLatest`, and `msgdb.Record`.
  - Hash-slot validation uses `pkg/cluster.HashSlotForKey`, matching the current inspect planner.
  - Message writes use `ChannelLog.ApplyFetch` with `BaseSeq = LEO + 1`, preserving source `message_seq` because validation enforces contiguous ordered records.
- Required implementation note:
  - Transfer record structs use the `Uint64` wrapper for every imported unsigned 64-bit field, and DB-write boundaries convert with `uint64(row.Field)`.
