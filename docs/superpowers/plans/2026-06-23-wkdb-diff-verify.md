# WKDB Diff Verify Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `wkdb diff` to compare two node-local WKDB stores and report whether an export/import migration preserved bundle-v1 data.

**Architecture:** Implement comparison in `pkg/db/transfer` over read-only `inspect.Store` handles, using `meta.InspectScan`, `message.InspectChannels`, and `message.InspectMessages`. Keep `cmd/wkdb` responsible only for CLI flags, opening source/target read-only stores, formatting the report, and mapping equal/mismatch/error exit codes.

**Tech Stack:** Go, existing `pkg/db/inspect`, `pkg/db/meta`, `pkg/db/message`, `pkg/db/transfer`, shell smoke scripts.

---

### Task 1: Transfer Verify Core

**Files:**
- Create: `pkg/db/transfer/verify.go`
- Create: `pkg/db/transfer/verify_test.go`

- [ ] **Step 1: Write the failing equal-store test**

Add `TestVerifyStoresReportsEqualForRoundTripStores` in `pkg/db/transfer/verify_test.go`.

Test shape:

```go
func TestVerifyStoresReportsEqualForRoundTripStores(t *testing.T) {
    ctx := context.Background()
    source, sourceOpts := seedVerifyNodeStore(t, 16)
    closeStore(t, source)

    sourceInspect := openVerifyInspectStore(t, sourceOpts, 16)
    exportRoot := filepath.Join(t.TempDir(), "bundle")
    if _, err := ExportBundle(ctx, exportRoot, sourceInspect, ExportOptions{HashSlotCount: 16, PageSize: 1, MessageFileRows: 1}); err != nil {
        t.Fatalf("ExportBundle(): %v", err)
    }
    target := openImportNodeStore(t)
    if _, err := ImportBundle(ctx, exportRoot, target, ImportOptions{HashSlotCount: 16, RequireEmpty: true}); err != nil {
        t.Fatalf("ImportBundle(): %v", err)
    }
    targetOpts := target.Options()
    closeStore(t, target)

    report, err := VerifyStores(ctx, openVerifyInspectStore(t, sourceOpts, 16), openVerifyInspectStore(t, targetOpts, 16), VerifyOptions{
        HashSlotCount: 16,
        PageSize: 1,
        Mode: VerifyModeFull,
    })
    if err != nil {
        t.Fatalf("VerifyStores(): %v", err)
    }
    if !report.Equal {
        t.Fatalf("report.Equal = false: %+v", report)
    }
}
```

Use real stores and include user, device, channel, subscriber, membership, conversation, channel_latest, and at least two messages.

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/db/transfer -run TestVerifyStoresReportsEqualForRoundTripStores -count=1
```

Expected: build failure with `undefined: VerifyStores`.

- [ ] **Step 3: Implement public types and option normalization**

Create `pkg/db/transfer/verify.go` with:

```go
type VerifyMode string

const (
    VerifyModeSummary VerifyMode = "summary"
    VerifyModeFull    VerifyMode = "full"
)

type VerifyOptions struct {
    HashSlotCount uint16
    PageSize int
    Mode VerifyMode
    MaxMismatches int
}

type VerifyDatasetReport struct {
    Name string `json:"name"`
    Equal bool `json:"equal"`
    SourceRows int64 `json:"source_rows"`
    TargetRows int64 `json:"target_rows"`
    SourceDigest string `json:"source_digest"`
    TargetDigest string `json:"target_digest"`
}

type VerifyMismatch struct {
    Scope string `json:"scope"`
    Detail string `json:"detail"`
}

type VerifyReport struct {
    Equal bool `json:"equal"`
    Mode VerifyMode `json:"mode"`
    HashSlotCount uint16 `json:"hash_slot_count"`
    Meta []VerifyDatasetReport `json:"meta"`
    Message []VerifyDatasetReport `json:"message"`
    Mismatches []VerifyMismatch `json:"mismatches,omitempty"`
}
```

Normalize defaults:

- `Mode` default `summary`.
- `PageSize` default `1000`.
- `MaxMismatches` default `20`.
- reject zero `HashSlotCount`.
- reject unknown mode.

- [ ] **Step 4: Implement metadata digest comparison**

Add helper functions:

```go
func VerifyStores(ctx context.Context, source, target *inspect.Store, opts VerifyOptions) (VerifyReport, error)
func verifyMetaTable(ctx context.Context, source, target *metadb.MetaDB, opts VerifyOptions, table string) (VerifyDatasetReport, []VerifyMismatch, error)
func scanMetaDigest(ctx context.Context, db *metadb.MetaDB, opts VerifyOptions, table string) (rows int64, digest string, err error)
```

Scan each table slot-by-slot with `metadb.InspectScan`. Canonicalize rows by writing a JSON object with fields:

```go
map[string]any{
    "hash_slot": slot,
    "row": row,
}
```

Encode to SHA256 through `json.Encoder` with stable key order by converting the map into a small struct:

```go
type digestMetaRow struct {
    HashSlot uint16 `json:"hash_slot"`
    Row metadb.InspectRow `json:"row"`
}
```

- [ ] **Step 5: Implement message catalog and message digest comparison**

Add:

```go
func verifyMessageCatalog(ctx context.Context, source, target *msgdb.MessageDB, opts VerifyOptions) (VerifyDatasetReport, []VerifyMismatch, error)
func verifyMessages(ctx context.Context, source, target *msgdb.MessageDB, opts VerifyOptions) (VerifyDatasetReport, []VerifyMismatch, error)
```

For summary mode, digest message rows with:

```go
type digestMessageSummaryRow struct {
    ChannelKey string `json:"channel_key"`
    MessageSeq uint64 `json:"message_seq"`
    MessageID uint64 `json:"message_id"`
    ClientMsgNo string `json:"client_msg_no"`
    FromUID string `json:"from_uid"`
    ServerTimestampMS int64 `json:"server_timestamp_ms"`
    PayloadHash uint64 `json:"payload_hash"`
    PayloadSize uint64 `json:"payload_size"`
}
```

For full mode, add `Payload []byte`.

Scan source and target separately for first version; compare total row counts and digests. Keep mismatch details bounded to count/digest messages rather than storing row-level differences.

- [ ] **Step 6: Run transfer tests**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/db/transfer -run 'TestVerifyStores' -count=1
```

Expected: equal-store test passes.

- [ ] **Step 7: Add mismatch tests**

Add tests:

```go
func TestVerifyStoresReportsMetaMismatch(t *testing.T)
func TestVerifyStoresReportsMessageMismatch(t *testing.T)
func TestVerifyStoresRejectsUnknownMode(t *testing.T)
```

Use real target stores with one changed user token or one changed message payload. Assert `report.Equal == false`, exit is not involved at transfer layer, and `len(report.Mismatches) > 0`.

- [ ] **Step 8: Run transfer package**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/db/transfer -count=1
```

- [ ] **Step 9: Commit**

```bash
git add pkg/db/transfer/verify.go pkg/db/transfer/verify_test.go
git commit -m "feat(wkdb): verify store equality"
```

### Task 2: `wkdb diff` CLI

**Files:**
- Create: `cmd/wkdb/diff.go`
- Modify: `cmd/wkdb/main.go`
- Modify: `cmd/wkdb/main_test.go`

- [ ] **Step 1: Write the failing equal CLI test**

Add `TestDiffCommandReturnsOKForEqualStores` in `cmd/wkdb/main_test.go`.

Test flow:

1. Create a minimal import bundle with existing helper `writeMinimalImportBundle`.
2. Import it into `sourceDir`.
3. Export from `sourceDir`.
4. Import exported bundle into `targetDir`.
5. Run:

```go
code := runWithStreams([]string{
    "--hash-slot-count", "16",
    "diff",
    "--source-data-dir", sourceDir,
    "--target-data-dir", targetDir,
    "--mode", "full",
}, nil, &stdout, &stderr)
```

Assert `code == exitOK`, stderr empty, stdout contains `equal=true`.

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./cmd/wkdb -run TestDiffCommandReturnsOKForEqualStores -count=1
```

Expected: failure with unknown command `diff`.

- [ ] **Step 3: Implement path flag parsing**

Create `cmd/wkdb/diff.go` with:

```go
type diffCommandFlags struct {
    sourceDataDir string
    sourceMetaPath string
    sourceMessagePath string
    targetDataDir string
    targetMetaPath string
    targetMessagePath string
    mode string
    pageSize int
}
```

Resolve paths using the same directory convention as `--data-dir`:

- metadata: `<data-dir>/data`
- message: `<data-dir>/channellog`

Do not reuse global `--data-dir` for source/target because diff needs two stores.

- [ ] **Step 4: Implement CLI execution**

Add `runDiff`:

```go
func runDiff(ctx context.Context, global cliFlags, args []string, stdout, stderr io.Writer) int
```

Behavior:

- require `--hash-slot-count` or config-derived hash slot count.
- require source meta/message paths.
- require target meta/message paths.
- open both stores through `inspect.OpenStore`.
- call `transfer.VerifyStores`.
- render report based on global `--format`.
- return `exitOK` when `report.Equal`.
- return `exitQuery` when `!report.Equal`.

Wire `diff` into `runWithStreams`.

- [ ] **Step 5: Implement compact report rendering**

For table format print:

```text
equal=true mode=full meta=7 message=2 mismatches=0
```

For JSON use `json.Encoder` on `VerifyReport`.

For JSONL write:

```json
{"type":"summary","equal":true,"mode":"full"}
{"type":"meta","report":{...}}
{"type":"message","report":{...}}
{"type":"mismatch","mismatch":{...}}
```

- [ ] **Step 6: Add mismatch CLI test**

Add `TestDiffCommandReturnsQueryExitForMismatch`. Seed source and target separately with different user token or message payload. Assert:

```go
if code != exitQuery { t.Fatalf("code = %d, want %d", code, exitQuery) }
if !strings.Contains(stdout.String(), "equal=false") { ... }
```

- [ ] **Step 7: Run CLI tests**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./cmd/wkdb -run 'TestDiffCommand' -count=1
GOWORK=off /usr/local/go/bin/go test ./cmd/wkdb -count=1
```

- [ ] **Step 8: Commit**

```bash
git add cmd/wkdb/diff.go cmd/wkdb/main.go cmd/wkdb/main_test.go
git commit -m "feat(wkdb): add diff command"
```

### Task 3: Smoke Script

**Files:**
- Create: `scripts/wkdb-diff-smoke.sh`
- Modify: `scripts/wkdb_import_smoke_script_test.go`

- [ ] **Step 1: Write the failing script test**

Add `TestWKDBDiffSmokeScript`:

```go
func TestWKDBDiffSmokeScript(t *testing.T) {
    root := repoRoot(t)
    wkdbBin := filepath.Join(t.TempDir(), "wkdb")
    build := exec.Command(goTool(t), "build", "-o", wkdbBin, "./cmd/wkdb")
    build.Dir = root
    build.Env = append(os.Environ(), "GOWORK=off")
    if output, err := build.CombinedOutput(); err != nil {
        t.Fatalf("build wkdb failed: %v\n%s", err, output)
    }

    workDir := t.TempDir()
    cmd := exec.Command("bash", "scripts/wkdb-diff-smoke.sh", "--work-dir", workDir)
    cmd.Dir = root
    cmd.Env = append(os.Environ(), "WKDB_BIN="+wkdbBin)
    output, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("diff smoke script failed: %v\n%s", err, output)
    }
    text := string(output)
    for _, want := range []string{"diff equal ok", "diff mismatch ok", "wkdb diff smoke passed"} {
        if !strings.Contains(text, want) {
            t.Fatalf("script output missing %q:\n%s", want, text)
        }
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./scripts -run TestWKDBDiffSmokeScript -count=1
```

Expected: failure because `scripts/wkdb-diff-smoke.sh` does not exist.

- [ ] **Step 3: Implement smoke script**

Script flow:

1. Use `scripts/wkdb-import-smoke.sh --work-dir "$seed_work_dir"` to create source data.
2. Run `wkdb export` from source to bundle.
3. Run `wkdb import` into target.
4. Run `wkdb diff --mode full`; expect exit `0`; print `diff equal ok`.
5. Create a mismatch target by importing the original seed bundle and then importing should not merge. For a simple mismatch without adding write APIs, create a second target from a copied bundle where `meta/users.jsonl` token is changed before import.
6. Run `wkdb diff`; expect exit `2`; print `diff mismatch ok`.
7. Print `wkdb diff smoke passed`.

- [ ] **Step 4: Run script tests**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./scripts -run 'TestWKDB(ImportSmoke|ExportRoundTrip|DiffSmoke)' -count=1
```

- [ ] **Step 5: Commit**

```bash
git add scripts/wkdb-diff-smoke.sh scripts/wkdb_import_smoke_script_test.go
git commit -m "test(wkdb): add diff smoke"
```

### Task 4: Documentation and Final Verification

**Files:**
- Modify: `cmd/wkdb/README.md`
- Modify: `docs/wiki/operations/wkdb-readonly-cli.md`

- [ ] **Step 1: Update CLI README**

Document:

```bash
wkdb --hash-slot-count 256 diff --source-data-dir ./node-old --target-data-dir ./node-new
wkdb --hash-slot-count 256 diff --source-data-dir ./node-old --target-data-dir ./node-new --mode full
```

Include:

- `summary` is default.
- `full` hashes payload bytes.
- exit `0` means equal, exit `2` means mismatch.
- source/target stores are read-only.

- [ ] **Step 2: Update wiki**

Add a `离线 diff/verify` section after export/import sections with:

- purpose: migration verification.
- path flags.
- mode semantics.
- performance note for large message stores.
- non-goals: no cross-node aggregation, no repair.

- [ ] **Step 3: Run focused verification**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/db/message ./pkg/db/inspect ./pkg/db/transfer ./cmd/wkdb -count=1
GOWORK=off /usr/local/go/bin/go test ./scripts -run 'TestWKDB(ImportSmoke|ExportRoundTrip|DiffSmoke)' -count=1
GO=/usr/local/go/bin/go scripts/wkdb-diff-smoke.sh --work-dir /tmp/wkdb-diff-smoke-main
```

- [ ] **Step 4: Commit**

```bash
git add cmd/wkdb/README.md docs/wiki/operations/wkdb-readonly-cli.md
git commit -m "docs(wkdb): document diff verification"
```

### Task 5: Merge Back to Main

**Files:** none

- [ ] **Step 1: Check dirty paths on main**

Run:

```bash
git -C /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM diff --name-only
git -C /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM diff --name-only main...codex/wkdb-diff-plan
```

Confirm there is no overlap with existing monitor/web draft files.

- [ ] **Step 2: Fast-forward merge**

Run:

```bash
git -C /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM merge --ff-only codex/wkdb-diff-plan
```

- [ ] **Step 3: Re-run final verification on main**

Run the same focused verification commands from Task 4 Step 3 in the main checkout.

- [ ] **Step 4: Clean worktree**

Run:

```bash
git -C /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM worktree remove .worktrees/codex/wkdb-diff-plan
git -C /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM branch -d codex/wkdb-diff-plan
git -C /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM worktree prune
```
